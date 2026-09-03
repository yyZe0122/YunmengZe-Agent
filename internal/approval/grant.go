package approval

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/pathsecurity"
	"github.com/yyZe0122/yunmengze-agent/pkg/eventapi"
	"github.com/yyZe0122/yunmengze-agent/pkg/sqliteerror"
)

var (
	ErrGrantDenied    = errors.New("capability grant denied")
	ErrGrantExpired   = errors.New("capability grant expired")
	ErrGrantExhausted = errors.New("capability grant exhausted")
	ErrGrantRevoked   = errors.New("capability grant revoked")
)

type CapabilityGrant struct {
	ID         GrantID
	ApprovalID ApprovalID
	TaskID     kernel.TaskID
	PlanID     kernel.PlanID
	StepID     kernel.StepID
	PlanHash   string
	Scope      CapabilityScope
	IssuedAt   time.Time
	ExpiresAt  time.Time
	UsedCalls  uint64
	RevokedAt  *time.Time
}

type GrantInput struct {
	ID         GrantID
	ApprovalID ApprovalID
	Plan       PlanDocument
	StepID     kernel.StepID
	Scope      CapabilityScope
	IssuedAt   time.Time
	ExpiresAt  time.Time
}

// IssueGrant only accepts a capability scope that is exactly present in the
// approved canonical Plan. A caller cannot widen an approval while minting a
// grant.
func (r *Repository) IssueGrant(ctx context.Context, input GrantInput) (CapabilityGrant, error) {
	if ctx == nil {
		return CapabilityGrant{}, errors.New("grant context is required")
	}
	if r == nil || r.db == nil {
		return CapabilityGrant{}, errors.New("approval repository is unavailable")
	}
	if strings.TrimSpace(string(input.ID)) == "" || strings.TrimSpace(string(input.ApprovalID)) == "" || strings.TrimSpace(string(input.StepID)) == "" {
		return CapabilityGrant{}, fmt.Errorf("%w: grant, approval, and step IDs are required", ErrGrantDenied)
	}
	plan, err := input.Plan.normalized()
	if err != nil {
		return CapabilityGrant{}, err
	}
	scope, err := normalizeCapability(input.Scope)
	if err != nil {
		return CapabilityGrant{}, err
	}
	if !planContainsScope(plan, input.StepID, scope) {
		return CapabilityGrant{}, fmt.Errorf("%w: scope is not present in approved plan", ErrGrantDenied)
	}
	issuedAt := normalizeTime(input.IssuedAt)
	expiresAt := input.ExpiresAt.UTC()
	if input.ExpiresAt.IsZero() || !expiresAt.After(issuedAt) {
		return CapabilityGrant{}, fmt.Errorf("%w: expiration must be after issue time", ErrGrantDenied)
	}
	planHash, err := plan.Hash()
	if err != nil {
		return CapabilityGrant{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return CapabilityGrant{}, fmt.Errorf("begin grant issue: %w", err)
	}
	defer tx.Rollback()
	if err := requireCurrentPlan(ctx, tx, plan, planHash); err != nil {
		return CapabilityGrant{}, err
	}
	if err := requireApprovedScope(ctx, tx, input.ApprovalID, plan, input.StepID, planHash, issuedAt, expiresAt); err != nil {
		return CapabilityGrant{}, err
	}

	pathsJSON, _ := json.Marshal(scope.Paths)
	argsJSON, _ := json.Marshal(scope.Arguments)
	domainsJSON, _ := json.Marshal(scope.NetworkDomains)
	resourceJSON, err := json.Marshal(scope)
	if err != nil {
		return CapabilityGrant{}, fmt.Errorf("marshal grant scope: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
        INSERT INTO capability_grants (
            grant_id, approval_id, task_id, plan_id, step_id, capability,
            resource_scope, issued_at, expires_at, revoked_at, plan_hash,
            paths_json, command_name, command_args_json, network_domains_json,
            max_duration_ms, max_calls, used_calls, one_time, updated_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		input.ID, input.ApprovalID, plan.TaskID, plan.PlanID, input.StepID, scope.Capability,
		string(resourceJSON), formatTime(issuedAt), formatTime(expiresAt), planHash,
		string(pathsJSON), scope.Command, string(argsJSON), string(domainsJSON),
		scope.MaxDurationMillis, scope.MaxCalls, scope.OneTime, formatTime(issuedAt),
	)
	if err != nil {
		if sqliteerror.IsUniqueConstraint(err) {
			return CapabilityGrant{}, fmt.Errorf("%w: %s", ErrAlreadyExists, input.ID)
		}
		return CapabilityGrant{}, fmt.Errorf("insert capability grant: %w", err)
	}

	grant := CapabilityGrant{
		ID: input.ID, ApprovalID: input.ApprovalID, TaskID: plan.TaskID,
		PlanID: plan.PlanID, StepID: input.StepID, PlanHash: planHash,
		Scope: scope, IssuedAt: issuedAt, ExpiresAt: expiresAt,
	}
	event, err := grantEvent(grant, "capability_grant.issued", 1, issuedAt, nil)
	if err != nil {
		return CapabilityGrant{}, err
	}
	if _, err := r.events.AppendTx(ctx, tx, event); err != nil {
		return CapabilityGrant{}, fmt.Errorf("append grant issue event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CapabilityGrant{}, fmt.Errorf("commit grant issue: %w", err)
	}
	return grant, nil
}

// AppendGrantPath adds extraRoot to every non-revoked path-scoped grant for the task
// so the current turn can keep using existing fs_*/git_*/process grants after extra-root /perm.
func (r *Repository) AppendGrantPath(ctx context.Context, taskID kernel.TaskID, extraRoot string) error {
	if ctx == nil {
		return errors.New("grant context is required")
	}
	if r == nil || r.db == nil {
		return errors.New("approval repository is unavailable")
	}
	extraRoot = strings.TrimSpace(extraRoot)
	if extraRoot == "" {
		return errors.New("extra root is required")
	}
	if strings.TrimSpace(string(taskID)) == "" {
		return errors.New("task id is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin append grant path: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT grant_id, paths_json FROM capability_grants
		WHERE task_id = ? AND revoked_at IS NULL`, taskID)
	if err != nil {
		return fmt.Errorf("list task grants: %w", err)
	}
	defer rows.Close()
	type row struct {
		id    string
		paths []string
	}
	var updates []row
	for rows.Next() {
		var id, pathsJSON string
		if err := rows.Scan(&id, &pathsJSON); err != nil {
			return err
		}
		var paths []string
		if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
			return fmt.Errorf("decode grant paths: %w", err)
		}
		if len(paths) == 0 {
			continue
		}
		found := false
		for _, p := range paths {
			if strings.TrimSpace(p) == extraRoot {
				found = true
				break
			}
		}
		if found {
			continue
		}
		updates = append(updates, row{id: id, paths: append(paths, extraRoot)})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, u := range updates {
		encoded, err := json.Marshal(u.paths)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE capability_grants SET paths_json = ?, updated_at = ? WHERE grant_id = ?`,
			string(encoded), now, u.id); err != nil {
			return fmt.Errorf("append grant path: %w", err)
		}
	}
	return tx.Commit()
}

type GrantRequest struct {
	GrantID       GrantID
	TaskID        kernel.TaskID
	PlanID        kernel.PlanID
	StepID        kernel.StepID
	PlanHash      string
	Capability    string
	Path          string
	Command       string
	Arguments     []string
	NetworkDomain string
	Duration      time.Duration
	Now           time.Time
}

// AuthorizeAndConsume performs all immutable scope checks and atomically
// consumes one call. Failure never increments the call counter.
func (r *Repository) AuthorizeAndConsume(ctx context.Context, request GrantRequest) error {
	if ctx == nil {
		return errors.New("grant context is required")
	}
	if r == nil || r.db == nil {
		return errors.New("approval repository is unavailable")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin grant authorization: %w", err)
	}
	defer tx.Rollback()
	if err := r.AuthorizeAndConsumeTx(ctx, tx, request); err != nil {
		return err
	}
	return tx.Commit()
}

// AuthorizeAndConsumeTx performs grant checks and consumption in the caller's
// transaction so the caller can persist the authorized operation atomically.
func (r *Repository) AuthorizeAndConsumeTx(ctx context.Context, tx *sql.Tx, request GrantRequest) error {
	if ctx == nil {
		return errors.New("grant context is required")
	}
	if r == nil || r.db == nil {
		return errors.New("approval repository is unavailable")
	}
	if tx == nil {
		return errors.New("grant transaction is required")
	}
	now := normalizeTime(request.Now)

	grant, err := loadGrant(ctx, tx, request.GrantID)
	if err != nil {
		return err
	}
	if grant.RevokedAt != nil {
		return ErrGrantRevoked
	}
	if !now.Before(grant.ExpiresAt) {
		return ErrGrantExpired
	}
	if grant.UsedCalls >= grant.Scope.MaxCalls {
		return ErrGrantExhausted
	}
	if err := grantMatchesRequest(grant, request); err != nil {
		return err
	}

	var currentHash, taskState string
	if err := tx.QueryRowContext(ctx, "SELECT p.scope_hash, t.state FROM plans p JOIN tasks t ON t.task_id = p.task_id WHERE p.plan_id = ?", grant.PlanID).Scan(&currentHash, &taskState); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return kernel.ErrNotFound
		}
		return fmt.Errorf("query grant plan: %w", err)
	}
	if currentHash != grant.PlanHash {
		return fmt.Errorf("%w: current plan hash differs", ErrPlanChanged)
	}
	if taskState != string(kernel.TaskRunning) {
		return fmt.Errorf("%w: task is %s", ErrGrantDenied, taskState)
	}

	result, err := tx.ExecContext(ctx, `
        UPDATE capability_grants
        SET used_calls = used_calls + 1, updated_at = ?
        WHERE grant_id = ? AND used_calls = ? AND used_calls < max_calls AND revoked_at IS NULL`,
		formatTime(now), grant.ID, grant.UsedCalls,
	)
	if err != nil {
		return fmt.Errorf("consume capability grant: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read consumed grant rows: %w", err)
	}
	if affected != 1 {
		return ErrGrantExhausted
	}
	return nil
}

func (r *Repository) RevokeGrant(ctx context.Context, grantID GrantID, now time.Time) error {
	if ctx == nil {
		return errors.New("grant context is required")
	}
	now = normalizeTime(now)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin grant revocation: %w", err)
	}
	defer tx.Rollback()
	grant, err := loadGrant(ctx, tx, grantID)
	if err != nil {
		return err
	}
	if grant.RevokedAt != nil {
		return ErrGrantRevoked
	}
	result, err := tx.ExecContext(ctx,
		"UPDATE capability_grants SET revoked_at = ?, updated_at = ? WHERE grant_id = ? AND revoked_at IS NULL",
		formatTime(now), formatTime(now), grantID,
	)
	if err != nil {
		return fmt.Errorf("revoke capability grant: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return ErrGrantRevoked
	}
	event, err := grantEvent(grant, "capability_grant.revoked", 2, now, map[string]any{"revoked_at": now})
	if err != nil {
		return err
	}
	if _, err := r.events.AppendTx(ctx, tx, event); err != nil {
		return fmt.Errorf("append grant revocation event: %w", err)
	}
	return tx.Commit()
}

// RevokeTaskGrants invalidates every still-active capability grant owned by a
// task. It is idempotent so a cancel request can be safely retried after an
// interrupted client connection.
func (r *Repository) RevokeTaskGrants(ctx context.Context, taskID kernel.TaskID, now time.Time) error {
	if ctx == nil {
		return errors.New("grant context is required")
	}
	if r == nil || r.db == nil {
		return errors.New("approval repository is unavailable")
	}
	if strings.TrimSpace(string(taskID)) == "" {
		return fmt.Errorf("%w: task ID is required", ErrGrantDenied)
	}
	now = normalizeTime(now)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin task grant revocation: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, "SELECT grant_id FROM capability_grants WHERE task_id = ? AND revoked_at IS NULL ORDER BY grant_id", taskID)
	if err != nil {
		return fmt.Errorf("list task grants: %w", err)
	}
	var grantIDs []GrantID
	for rows.Next() {
		var grantID GrantID
		if err := rows.Scan(&grantID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan task grant: %w", err)
		}
		grantIDs = append(grantIDs, grantID)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close task grants: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate task grants: %w", err)
	}

	for _, grantID := range grantIDs {
		grant, err := loadGrant(ctx, tx, grantID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE capability_grants SET revoked_at = ?, updated_at = ? WHERE grant_id = ? AND revoked_at IS NULL",
			formatTime(now), formatTime(now), grantID,
		); err != nil {
			return fmt.Errorf("revoke task grant: %w", err)
		}
		event, err := grantEvent(grant, "capability_grant.revoked", 2, now, map[string]any{"revoked_at": now, "reason": "task_cancelled"})
		if err != nil {
			return err
		}
		if _, err := r.events.AppendTx(ctx, tx, event); err != nil {
			return fmt.Errorf("append task grant revocation event: %w", err)
		}
	}
	return tx.Commit()
}

func requireApprovedScope(
	ctx context.Context,
	tx *sql.Tx,
	approvalID ApprovalID,
	plan PlanDocument,
	stepID kernel.StepID,
	planHash string,
	issuedAt time.Time,
	grantExpiresAt time.Time,
) error {
	var scope, decision, storedHash string
	var storedStep sql.NullString
	var revision int64
	var approvalExpires, invalidated sql.NullString
	err := tx.QueryRowContext(ctx, `
        SELECT scope_type, step_id, decision, scope_hash, plan_revision, expires_at, invalidated_at
        FROM approvals WHERE approval_id = ?`, approvalID,
	).Scan(&scope, &storedStep, &decision, &storedHash, &revision, &approvalExpires, &invalidated)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotApproved
	}
	if err != nil {
		return fmt.Errorf("query approval for grant: %w", err)
	}
	if decision != string(DecisionApproved) || invalidated.Valid || storedHash != planHash || revision != int64(plan.Revision) {
		return ErrNotApproved
	}
	if ScopeType(scope) == ScopeStep && (!storedStep.Valid || storedStep.String != string(stepID)) {
		return ErrNotApproved
	}
	if ScopeType(scope) != ScopePlan && ScopeType(scope) != ScopeStep {
		return ErrNotApproved
	}
	if approvalExpires.Valid {
		expiresAt, err := time.Parse(time.RFC3339Nano, approvalExpires.String)
		if err != nil {
			return fmt.Errorf("parse approval expiration: %w", err)
		}
		if !issuedAt.Before(expiresAt) || grantExpiresAt.After(expiresAt) {
			return ErrNotApproved
		}
	}
	return nil
}

func planContainsScope(plan PlanDocument, stepID kernel.StepID, scope CapabilityScope) bool {
	expected, _ := json.Marshal(scope)
	for _, step := range plan.Steps {
		if step.StepID != stepID {
			continue
		}
		for _, candidate := range step.Capabilities {
			encoded, _ := json.Marshal(candidate)
			if slices.Equal(encoded, expected) {
				return true
			}
			if hostCapabilityNarrowing(candidate, scope) {
				return true
			}
			if extraRootGrant(candidate, scope) {
				return true
			}
		}
	}
	return false
}

// hostCapabilityNarrowing allows issuing a grant whose only difference from a
// plan host-only scope (empty Paths) is filling empty NetworkDomains with a single host.
func hostCapabilityNarrowing(planScope, grantScope CapabilityScope) bool {
	switch planScope.Capability {
	case "http_get", "web_search", "web_extract", "vision_analyze":
	default:
		return false
	}
	if planScope.Capability != grantScope.Capability {
		return false
	}
	if len(planScope.NetworkDomains) != 0 || len(grantScope.NetworkDomains) != 1 {
		return false
	}
	if strings.TrimSpace(grantScope.NetworkDomains[0]) == "" {
		return false
	}
	if len(planScope.Paths) != 0 || len(grantScope.Paths) != 0 {
		return false
	}
	if planScope.OneTime != grantScope.OneTime || planScope.MaxCalls != grantScope.MaxCalls ||
		planScope.MaxDurationMillis != grantScope.MaxDurationMillis {
		return false
	}
	return true
}

// extraRootGrant allows a human-approved /perm grant whose only difference from a
// path-scoped plan capability is replacing Paths with a single extra absolute root.
func extraRootGrant(planScope, grantScope CapabilityScope) bool {
	if planScope.Capability != grantScope.Capability {
		return false
	}
	if len(planScope.Paths) == 0 || len(grantScope.Paths) != 1 {
		return false
	}
	extra := strings.TrimSpace(grantScope.Paths[0])
	if extra == "" {
		return false
	}
	for _, p := range planScope.Paths {
		if strings.TrimSpace(p) == extra {
			return false
		}
	}
	if planScope.MaxDurationMillis != grantScope.MaxDurationMillis {
		return false
	}
	if planScope.OneTime != grantScope.OneTime || planScope.MaxCalls != grantScope.MaxCalls {
		if !(grantScope.OneTime && grantScope.MaxCalls == 1 && !planScope.OneTime) {
			return false
		}
	}
	if planScope.Command != grantScope.Command {
		return false
	}
	if !slices.Equal(planScope.Arguments, grantScope.Arguments) {
		return false
	}
	if !slices.Equal(planScope.NetworkDomains, grantScope.NetworkDomains) {
		return false
	}
	return true
}

func loadGrant(ctx context.Context, tx *sql.Tx, grantID GrantID) (CapabilityGrant, error) {
	if strings.TrimSpace(string(grantID)) == "" {
		return CapabilityGrant{}, fmt.Errorf("%w: grant ID is required", ErrGrantDenied)
	}
	var grant CapabilityGrant
	var taskID, planID, stepID, capability, planHash string
	var pathsJSON, command, argsJSON, domainsJSON string
	var issuedAt, expiresAt string
	var revokedAt sql.NullString
	var maxDuration int64
	var maxCalls, usedCalls int64
	var oneTime bool
	err := tx.QueryRowContext(ctx, `
        SELECT approval_id, task_id, plan_id, step_id, capability, plan_hash,
               paths_json, command_name, command_args_json, network_domains_json,
               max_duration_ms, max_calls, used_calls, one_time,
               issued_at, expires_at, revoked_at
        FROM capability_grants WHERE grant_id = ?`, grantID,
	).Scan(
		&grant.ApprovalID, &taskID, &planID, &stepID, &capability, &planHash,
		&pathsJSON, &command, &argsJSON, &domainsJSON,
		&maxDuration, &maxCalls, &usedCalls, &oneTime,
		&issuedAt, &expiresAt, &revokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CapabilityGrant{}, fmt.Errorf("%w: unknown grant", ErrGrantDenied)
	}
	if err != nil {
		return CapabilityGrant{}, fmt.Errorf("query capability grant: %w", err)
	}
	if maxCalls < 0 || usedCalls < 0 {
		return CapabilityGrant{}, fmt.Errorf("%w: invalid persisted call count", ErrGrantDenied)
	}
	grant.ID = grantID
	grant.TaskID = kernel.TaskID(taskID)
	grant.PlanID = kernel.PlanID(planID)
	grant.StepID = kernel.StepID(stepID)
	grant.PlanHash = planHash
	grant.Scope = CapabilityScope{
		Capability: capability, Command: command, MaxDurationMillis: maxDuration,
		MaxCalls: uint64(maxCalls), OneTime: oneTime,
	}
	if err := json.Unmarshal([]byte(pathsJSON), &grant.Scope.Paths); err != nil {
		return CapabilityGrant{}, fmt.Errorf("decode grant paths: %w", err)
	}
	if err := json.Unmarshal([]byte(argsJSON), &grant.Scope.Arguments); err != nil {
		return CapabilityGrant{}, fmt.Errorf("decode grant arguments: %w", err)
	}
	if err := json.Unmarshal([]byte(domainsJSON), &grant.Scope.NetworkDomains); err != nil {
		return CapabilityGrant{}, fmt.Errorf("decode grant domains: %w", err)
	}
	grant.UsedCalls = uint64(usedCalls)
	var parseErr error
	grant.IssuedAt, parseErr = time.Parse(time.RFC3339Nano, issuedAt)
	if parseErr != nil {
		return CapabilityGrant{}, fmt.Errorf("parse grant issue time: %w", parseErr)
	}
	grant.IssuedAt = grant.IssuedAt.UTC()
	grant.ExpiresAt, parseErr = time.Parse(time.RFC3339Nano, expiresAt)
	if parseErr != nil {
		return CapabilityGrant{}, fmt.Errorf("parse grant expiration: %w", parseErr)
	}
	grant.ExpiresAt = grant.ExpiresAt.UTC()
	if revokedAt.Valid {
		value, err := time.Parse(time.RFC3339Nano, revokedAt.String)
		if err != nil {
			return CapabilityGrant{}, fmt.Errorf("parse grant revocation: %w", err)
		}
		value = value.UTC()
		grant.RevokedAt = &value
	}
	return grant, nil
}

func grantMatchesRequest(grant CapabilityGrant, request GrantRequest) error {
	if request.TaskID != grant.TaskID || request.PlanID != grant.PlanID || request.StepID != grant.StepID {
		return fmt.Errorf("%w: task, plan, or step does not match", ErrGrantDenied)
	}
	if strings.TrimSpace(request.PlanHash) == "" || request.PlanHash != grant.PlanHash {
		return fmt.Errorf("%w: plan hash does not match", ErrGrantDenied)
	}
	if strings.TrimSpace(request.Capability) != grant.Scope.Capability {
		return fmt.Errorf("%w: capability does not match", ErrGrantDenied)
	}
	if request.Duration <= 0 || request.Duration.Milliseconds() > grant.Scope.MaxDurationMillis {
		return fmt.Errorf("%w: duration exceeds grant", ErrGrantDenied)
	}
	if !pathAllowed(grant.Scope.Paths, request.Path) {
		return fmt.Errorf("%w: path is outside grant", ErrGrantDenied)
	}
	// Scheme A (P4.3): empty grant command/args = any request command/args within path scope.
	// Non-empty grant command must match; non-empty grant args match exactly or as a prefix (QD3 similar).
	if err := commandArgsMatch(grant.Scope.Command, grant.Scope.Arguments, request.Command, request.Arguments); err != nil {
		return err
	}
	if !domainAllowed(grant.Scope.NetworkDomains, request.NetworkDomain) {
		return fmt.Errorf("%w: network domain is outside grant", ErrGrantDenied)
	}
	return nil
}

// commandArgsMatch implements grant command/args rules for scheme A:
// empty grant command accepts any request command; empty grant args accept any request args.
// When grant command is set it must equal the request; when grant args is non-empty it must equal request args.
func commandArgsMatch(grantCmd string, grantArgs []string, requestCmd string, requestArgs []string) error {
	requestCmd = strings.TrimSpace(requestCmd)
	if grantCmd != "" && grantCmd != requestCmd {
		return fmt.Errorf("%w: command or arguments do not match", ErrGrantDenied)
	}
	if len(grantArgs) == 0 {
		return nil
	}
	if slices.Equal(grantArgs, requestArgs) {
		return nil
	}
	if argsMatchPrefix(grantArgs, requestArgs) {
		return nil
	}
	return fmt.Errorf("%w: command or arguments do not match", ErrGrantDenied)
}

// argsMatchPrefix implements Claude-style Bash(go test *): grant args are a prefix
// of the request, and the last grant token may be a string prefix of that request arg.
func argsMatchPrefix(grantArgs, requestArgs []string) bool {
	if len(grantArgs) > len(requestArgs) || len(grantArgs) == 0 {
		return false
	}
	for i := 0; i < len(grantArgs)-1; i++ {
		if grantArgs[i] != requestArgs[i] {
			return false
		}
	}
	last := grantArgs[len(grantArgs)-1]
	got := requestArgs[len(grantArgs)-1]
	if last == got {
		return true
	}
	return strings.HasPrefix(got, last+" ") || strings.HasPrefix(got, last)
}

func pathAllowed(scopes []string, requested string) bool {
	requested = strings.TrimSpace(requested)
	if len(scopes) == 0 {
		return requested == ""
	}
	if requested == "" {
		return false
	}
	for _, scope := range scopes {
		if pathsecurity.ContainsResolved(filepath.FromSlash(scope), filepath.FromSlash(requested)) {
			return true
		}
	}
	return false
}

func domainAllowed(scopes []string, requested string) bool {
	requested = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(requested), "."))
	if len(scopes) == 0 {
		return requested == ""
	}
	if requested == "" {
		return false
	}
	for _, scope := range scopes {
		if requested == scope {
			return true
		}
		if strings.HasPrefix(scope, "*.") {
			suffix := strings.TrimPrefix(scope, "*")
			if strings.HasSuffix(requested, suffix) && requested != strings.TrimPrefix(suffix, ".") {
				return true
			}
		}
	}
	return false
}

func grantEvent(grant CapabilityGrant, eventType string, version uint64, occurredAt time.Time, extra map[string]any) (eventapi.Envelope, error) {
	payload := map[string]any{
		"approval_id": grant.ApprovalID,
		"task_id":     grant.TaskID,
		"plan_id":     grant.PlanID,
		"step_id":     grant.StepID,
		"plan_hash":   grant.PlanHash,
		"capability":  grant.Scope.Capability,
		"expires_at":  grant.ExpiresAt,
		"max_calls":   grant.Scope.MaxCalls,
		"one_time":    grant.Scope.OneTime,
	}
	for key, value := range extra {
		payload[key] = value
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return eventapi.Envelope{}, fmt.Errorf("marshal grant event: %w", err)
	}
	return eventapi.Envelope{
		ID:   fmt.Sprintf("capability_grant/%s/v/%d", grant.ID, version),
		Type: eventType, AggregateType: "capability_grant", AggregateID: string(grant.ID),
		AggregateVersion: version, OccurredAt: occurredAt, Producer: "approval",
		SchemaVersion: 1, Payload: encoded,
	}, nil
}
