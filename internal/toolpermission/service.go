package toolpermission

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/approval"
	"github.com/yyZe0122/yunmengze-agent/internal/audit"
	"github.com/yyZe0122/yunmengze-agent/internal/events"
	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/pathsecurity"
	"github.com/yyZe0122/yunmengze-agent/pkg/eventapi"
)

// ExtraRootExpander applies interactive extra-root after /perm (PathGuard + grants + optional config).
type ExtraRootExpander func(ctx context.Context, sessionID, taskID, extraRoot string, sessionPersist, configPersist bool, callID string) error

// Service decides pending tool permissions and issues scoped grants (ADR-043).
type Service struct {
	db        *sql.DB
	store     *Store
	approvals *approval.Repository
	waiter    *Waiter
	audit     *audit.Store
	events    *events.Store // optional; C1 permission SSE
	now       func() time.Time
	expand    ExtraRootExpander
}

type Config struct {
	DB        *sql.DB
	Store     *Store
	Approvals *approval.Repository
	Waiter    *Waiter
	Events    *events.Store // optional
	Now       func() time.Time
	Expand    ExtraRootExpander
}

func New(config Config) (*Service, error) {
	if config.DB == nil || config.Store == nil || config.Approvals == nil {
		return nil, errors.New("toolpermission requires db, store, and approvals")
	}
	if config.Waiter == nil {
		config.Waiter = NewWaiter()
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	auditStore, err := audit.NewStore(config.DB)
	if err != nil {
		return nil, err
	}
	return &Service{
		db: config.DB, store: config.Store, approvals: config.Approvals,
		waiter: config.Waiter, audit: auditStore, events: config.Events, now: config.Now,
		expand: config.Expand,
	}, nil
}

func (s *Service) SetExpand(expand ExtraRootExpander) {
	if s != nil {
		s.expand = expand
	}
}

// EmitPending publishes permission.pending for Gateway SSE (best-effort).
func (s *Service) EmitPending(ctx context.Context, req Request) {
	if s == nil || s.events == nil {
		return
	}
	s.emit(ctx, "permission.pending", req, "")
}

func (s *Service) emit(ctx context.Context, typ string, req Request, decision string) {
	if s == nil || s.events == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	payload := map[string]any{
		"permission_id": req.ID,
		"session_id":    req.SessionID,
		"task_id":       req.TaskID,
		"run_id":        req.RunID,
		"tool":          req.ToolName,
		"state":         req.State,
	}
	if decision != "" {
		payload["decision"] = decision
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	aggID := req.SessionID
	if aggID == "" {
		aggID = req.ID
	}
	_, _ = s.events.Append(ctx, eventapi.Envelope{
		ID:               fmt.Sprintf("permission/%s/%s/%d", typ, req.ID, s.now().UnixNano()),
		Type:             typ,
		AggregateType:    "tool_permission",
		AggregateID:      aggID,
		AggregateVersion: 1,
		OccurredAt:       s.now().UTC(),
		Producer:         "toolpermission",
		SchemaVersion:    1,
		Payload:          raw,
	})
}

func (s *Service) Waiter() *Waiter {
	if s == nil {
		return nil
	}
	return s.waiter
}

func (s *Service) Store() *Store {
	if s == nil {
		return nil
	}
	return s.store
}

// ListPending returns pending permission requests.
func (s *Service) ListPending(ctx context.Context, sessionID string, limit int) ([]Request, error) {
	items, err := s.store.ListPending(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].ExtraRoot = s.pendingExtraRoot(ctx, items[i])
	}
	return items, nil
}

func (s *Service) pendingExtraRoot(ctx context.Context, req Request) bool {
	if s == nil || strings.TrimSpace(req.PlanID) == "" || strings.TrimSpace(req.Path) == "" {
		return false
	}
	plan, err := s.loadPlanDocument(ctx, req.PlanID)
	if err != nil {
		return false
	}
	scope, err := findPlanScope(plan, kernel.StepID(req.StepID), req.Capability, req.ToolName, DecisionAllowOnce, req.Path, req.NetworkDomain)
	if err != nil {
		return false
	}
	extra, _, err := extraRootDecision(scope, req)
	return extra && err == nil
}

// HabitHint is a read-only suggestion from prior decides (H4). Never auto-applied.
type HabitHint struct {
	Decision string
	Reason   string
}

// SuggestHabit returns once/similar (or deny-reason only) from prior decides.
func (s *Service) SuggestHabit(ctx context.Context, req Request) HabitHint {
	if s == nil || s.store == nil {
		return HabitHint{}
	}
	priors, err := s.store.ListRecentDecisions(ctx, req.ToolName, req.Capability, req.SessionID, 8)
	if err != nil || len(priors) == 0 {
		return HabitHint{}
	}
	for _, p := range priors {
		if !pathRelated(req.Path, p.Path) {
			continue
		}
		switch p.Decision {
		case DecisionAllowSimilar:
			return HabitHint{Decision: DecisionAllowSimilar, Reason: "prior allow_similar on " + req.ToolName}
		case DecisionAllowOnce:
			return HabitHint{Decision: DecisionAllowOnce, Reason: "prior allow_once on " + req.ToolName}
		case DecisionDeny:
			return HabitHint{Reason: "prior deny on " + req.ToolName}
		}
	}
	return HabitHint{}
}

func pathRelated(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" && b == "" {
		return true
	}
	if a == "" || b == "" {
		return false
	}
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if a == b {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(a, b+sep) || strings.HasPrefix(b, a+sep)
}

// DecideOptions controls permanent confirm and trust persistence (ADR-046).
type DecideOptions struct {
	// Confirm must be true for allow_permanent (second confirmation).
	Confirm bool
	// TrustPath is optional absolute path to permissions-trust.json (ConfigDir).
	TrustPath string
}

// Decide applies allow_once | allow_similar | allow_permanent | deny.
func (s *Service) Decide(ctx context.Context, permissionID, decision, actor string) (Request, error) {
	return s.DecideWithOptions(ctx, permissionID, decision, actor, DecideOptions{})
}

// DecideWithOptions is Decide with permanent confirm + trust file support.
func (s *Service) DecideWithOptions(ctx context.Context, permissionID, decision, actor string, opts DecideOptions) (Request, error) {
	if ctx == nil {
		return Request{}, errors.New("context is required")
	}
	permissionID = strings.TrimSpace(permissionID)
	decision = strings.ToLower(strings.TrimSpace(decision))
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "user"
	}
	switch decision {
	case DecisionAllowOnce, DecisionAllowSimilar, DecisionAllowPermanent, DecisionDeny:
	default:
		return Request{}, fmt.Errorf("%w: must be allow_once, allow_similar, allow_permanent, or deny", ErrInvalidDecide)
	}
	if decision == DecisionAllowPermanent && !opts.Confirm {
		return Request{}, fmt.Errorf("%w: allow_permanent requires confirm=true (second confirmation)", ErrInvalidDecide)
	}
	req, err := s.store.Get(ctx, permissionID)
	if err != nil {
		return Request{}, err
	}
	if req.State != StatePending {
		return Request{}, ErrNotPending
	}

	now := s.now().UTC()
	decidedAt := now.Format(time.RFC3339Nano)
	grantID := ""
	state := StateDenied

	if decision == DecisionDeny {
		if err := s.store.MarkDecided(ctx, permissionID, decision, state, "", actor, decidedAt); err != nil {
			return Request{}, err
		}
		s.waiter.Notify(Decision{PermissionID: permissionID, Decision: decision, State: state})
		_ = s.audit.Record(ctx, audit.Entry{
			OccurredAt: now, Actor: actor, Action: "tool.permission.decide",
			ResourceType: "tool_permission", ResourceID: permissionID, Outcome: "denied",
			Details: map[string]any{"tool": req.ToolName, "decision": decision},
		})
		out, _ := s.store.Get(ctx, permissionID)
		s.emit(ctx, "permission.decided", out, decision)
		return out, nil
	}

	plan, err := s.loadPlanDocument(ctx, req.PlanID)
	if err != nil {
		return Request{}, err
	}
	// IssueGrant requires exact CapabilityScope present in the plan (ADR-011).
	// ask-mode plans embed once + session variants for high-risk tools.
	// similar/permanent use non-once (session) plan scopes.
	scope, err := findPlanScope(plan, kernel.StepID(req.StepID), req.Capability, req.ToolName, decision, req.Path, req.NetworkDomain)
	if err != nil {
		return Request{}, err
	}
	extra, extraRoot, extraErr := extraRootDecision(scope, req)
	if extraErr != nil {
		return Request{}, extraErr
	}
	if extra {
		if err := pathsecurity.ValidExtraRoot(extraRoot); err != nil {
			return Request{}, fmt.Errorf("%w: %v", ErrInvalidDecide, err)
		}
		if decision == DecisionAllowOnce && strings.TrimSpace(req.ToolCallID) == "" {
			return Request{}, fmt.Errorf("%w: extra-root once requires tool_call_id", ErrInvalidDecide)
		}
		if (decision == DecisionAllowSimilar || decision == DecisionAllowPermanent) && strings.TrimSpace(req.SessionID) == "" {
			return Request{}, fmt.Errorf("%w: extra-root similar/permanent requires session_id", ErrInvalidDecide)
		}
		scope.Paths = []string{extraRoot}
		if decision == DecisionAllowOnce {
			scope.OneTime = true
			scope.MaxCalls = 1
		}
	} else {
		scope = narrowScopeForSimilar(scope, req)
	}

	approvalID, err := s.lookupApprovalID(ctx, plan, req.PlanHash)
	if err != nil {
		return Request{}, err
	}
	grantID = "perm-grant-" + shortHash(permissionID, decision, scope.Capability)
	expiresAt := now.Add(time.Hour)
	switch decision {
	case DecisionAllowSimilar:
		expiresAt = now.Add(24 * time.Hour)
		state = StateAllowedSimilar
	case DecisionAllowPermanent:
		expiresAt = now.Add(365 * 24 * time.Hour)
		state = StateAllowedPermanent
	default:
		state = StateAllowedOnce
	}
	_, err = s.approvals.IssueGrant(ctx, approval.GrantInput{
		ID: approval.GrantID(grantID), ApprovalID: approval.ApprovalID(approvalID),
		Plan: plan, StepID: plan.Steps[0].StepID, Scope: scope,
		IssuedAt: now, ExpiresAt: expiresAt,
	})
	if err != nil && !errors.Is(err, approval.ErrAlreadyExists) {
		return Request{}, fmt.Errorf("issue permission grant: %w", err)
	}
	if extra && s.expand != nil {
		sessionPersist := decision == DecisionAllowSimilar || decision == DecisionAllowPermanent
		configPersist := decision == DecisionAllowPermanent
		if err := s.expand(ctx, req.SessionID, req.TaskID, extraRoot, sessionPersist, configPersist, req.ToolCallID); err != nil {
			return Request{}, fmt.Errorf("expand extra root: %w", err)
		}
	}
	if decision == DecisionAllowPermanent && strings.TrimSpace(opts.TrustPath) != "" {
		if err := AppendTrustEntry(opts.TrustPath, TrustEntry{
			Capability:  scope.Capability,
			PathPrefix:  firstPath(scope.Paths),
			Command:     scope.Command,
			ArgsPrefix:  append([]string(nil), scope.Arguments...),
			CreatedAt:   now.Format(time.RFC3339Nano),
			SessionHint: req.SessionID,
		}); err != nil {
			return Request{}, fmt.Errorf("persist permanent trust: %w", err)
		}
	}
	if err := s.store.MarkDecided(ctx, permissionID, decision, state, grantID, actor, decidedAt); err != nil {
		return Request{}, err
	}
	s.waiter.Notify(Decision{
		PermissionID: permissionID, Decision: decision, GrantID: grantID, State: state,
	})
	_ = s.audit.Record(ctx, audit.Entry{
		OccurredAt: now, Actor: actor, Action: "tool.permission.decide",
		ResourceType: "tool_permission", ResourceID: permissionID, Outcome: "allowed",
		Details: map[string]any{"tool": req.ToolName, "decision": decision, "grant_id": grantID},
	})
	slog.Info("tool permission decided",
		"component", "toolpermission", "operation", "decide", "result", "succeeded",
		"permission_id", permissionID, "decision", decision, "tool", req.ToolName,
		"session_id", req.SessionID, "task_id", req.TaskID, "run_id", req.RunID,
		"tool_call_id", req.ToolCallID)
	out, err := s.store.Get(ctx, permissionID)
	if err != nil {
		return out, err
	}
	s.emit(ctx, "permission.decided", out, decision)
	return out, nil
}

// narrowScopeForSimilar keeps plan capability but prefers the request path's parent
// when it still falls under the plan paths (session-pattern, ADR-046).
func narrowScopeForSimilar(scope approval.CapabilityScope, req Request) approval.CapabilityScope {
	if extra, root, err := extraRootDecision(scope, req); extra && err == nil {
		scope.Paths = []string{root}
		return scope
	}
	if req.Capability == "process_exec" || req.Capability == "process_shell" {
		scope = applyProcessSimilarPrefix(scope, req)
	}
	if req.Capability == "http_get" || req.Capability == "web_search" || req.Capability == "web_extract" || req.Capability == "vision_analyze" {
		if host := strings.TrimSpace(req.NetworkDomain); host != "" && len(scope.NetworkDomains) == 0 {
			scope.NetworkDomains = []string{strings.ToLower(host)}
		}
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return scope
	}
	// Use parent directory as prefix when path looks like a file; else the path itself.
	prefix := path
	if !strings.HasSuffix(path, "/") {
		// Keep full path for directory grants; parent for files is best-effort.
		if i := strings.LastIndex(path, "/"); i > 0 {
			// If request is under a plan root, grant that plan root only (already in scope).
			// Prefer the deepest plan path that contains the request path.
			for _, root := range scope.Paths {
				root = strings.TrimSpace(root)
				if root == "" {
					continue
				}
				if path == root || strings.HasPrefix(path, strings.TrimRight(root, "/")+"/") {
					prefix = root
					scope.Paths = []string{prefix}
					return scope
				}
			}
			prefix = path[:i]
		}
	}
	if len(scope.Paths) > 0 {
		// Only narrow if prefix is under an existing path.
		for _, root := range scope.Paths {
			root = strings.TrimSpace(root)
			if prefix == root || strings.HasPrefix(prefix, strings.TrimRight(root, "/")+"/") ||
				strings.HasPrefix(root, strings.TrimRight(prefix, "/")+"/") {
				scope.Paths = []string{prefix}
				return scope
			}
		}
	}
	return scope
}

func applyProcessSimilarPrefix(scope approval.CapabilityScope, req Request) approval.CapabilityScope {
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" {
		return scope
	}
	scope.Command = cmd
	args := append([]string(nil), req.CommandArgs...)
	if cmd == "/bin/sh" && len(args) >= 2 && args[0] == "-c" {
		script := strings.TrimSpace(args[1])
		if fields := strings.Fields(script); len(fields) >= 2 {
			scope.Arguments = []string{"-c", fields[0] + " " + fields[1]}
			return scope
		}
		scope.Arguments = []string{"-c", script}
		return scope
	}
	if len(args) >= 1 {
		scope.Arguments = args[:1]
		return scope
	}
	scope.Arguments = nil
	return scope
}

func extraRootDecision(scope approval.CapabilityScope, req Request) (bool, string, error) {
	path := strings.TrimSpace(req.Path)
	if path == "" || len(scope.Paths) == 0 {
		return false, "", nil
	}
	name := strings.TrimSpace(req.Capability)
	if name == "" {
		name = strings.TrimSpace(req.ToolName)
	}
	if !(strings.HasPrefix(name, "fs_") || strings.HasPrefix(name, "git_") || name == "process_exec" || name == "process_shell") {
		return false, "", nil
	}
	for _, root := range scope.Paths {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if path == root || strings.HasPrefix(path, strings.TrimRight(root, "/")+"/") {
			return false, "", nil
		}
	}
	root, err := extraRootFromPath(path)
	if err != nil {
		return false, "", fmt.Errorf("%w: %v", ErrInvalidDecide, err)
	}
	return true, root, nil
}

func extraRootFromPath(path string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		if err := pathsecurity.ValidExtraRoot(path); err != nil {
			return "", err
		}
		return path, nil
	}
	parent := filepath.Dir(path)
	if err := pathsecurity.ValidExtraRoot(parent); err == nil {
		return parent, nil
	}
	if err := pathsecurity.ValidExtraRoot(path); err != nil {
		return "", err
	}
	return path, nil
}

func firstPath(paths []string) string {
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" {
			return p
		}
	}
	return ""
}

// findPlanScope picks the plan capability matching tool + once/session variant.
// Path vs host: url requests (network domain, empty path) skip path-scoped entries so
// vision_analyze similar/permanent does not mint a Paths grant that then fails pathAllowed.
func findPlanScope(plan approval.PlanDocument, stepID kernel.StepID, capability, toolName, decision, path, networkDomain string) (approval.CapabilityScope, error) {
	capName := strings.TrimSpace(capability)
	if capName == "" {
		capName = strings.TrimSpace(toolName)
	}
	wantOnce := decision == DecisionAllowOnce
	wantHost := strings.TrimSpace(networkDomain) != "" && strings.TrimSpace(path) == ""
	wantPath := strings.TrimSpace(path) != ""
	try := func(filterStep bool) (approval.CapabilityScope, bool) {
		var fallback approval.CapabilityScope
		foundFallback := false
		for _, step := range plan.Steps {
			if filterStep && stepID != "" && step.StepID != stepID {
				continue
			}
			for _, c := range step.Capabilities {
				cn, err := approval.NormalizeCapabilityForPlan(c)
				if err != nil {
					continue
				}
				if cn.Capability != capName {
					continue
				}
				if wantHost && len(cn.Paths) != 0 {
					continue
				}
				if wantPath && len(cn.Paths) == 0 {
					continue
				}
				if wantOnce && cn.OneTime && cn.MaxCalls == 1 {
					return cn, true
				}
				if !wantOnce && !cn.OneTime {
					return cn, true
				}
				fallback = cn
				foundFallback = true
			}
		}
		if foundFallback {
			return fallback, true
		}
		return approval.CapabilityScope{}, false
	}
	if scope, ok := try(true); ok {
		return scope, nil
	}
	if stepID != "" {
		if scope, ok := try(false); ok {
			return scope, nil
		}
	}
	return approval.CapabilityScope{}, fmt.Errorf("%w: no plan capability %q for %s (interactive Agent plan must embed process/git scopes)", ErrInvalidDecide, capName, decision)
}

func (s *Service) loadPlanDocument(ctx context.Context, planID string) (approval.PlanDocument, error) {
	var document string
	err := s.db.QueryRowContext(ctx, `SELECT document FROM plans WHERE plan_id = ?`, planID).Scan(&document)
	if errors.Is(err, sql.ErrNoRows) {
		return approval.PlanDocument{}, fmt.Errorf("%w: plan not found", ErrNotFound)
	}
	if err != nil {
		return approval.PlanDocument{}, err
	}
	var plan approval.PlanDocument
	if err := json.Unmarshal([]byte(document), &plan); err != nil {
		return approval.PlanDocument{}, fmt.Errorf("decode plan: %w", err)
	}
	return plan, nil
}

func (s *Service) lookupApprovalID(ctx context.Context, plan approval.PlanDocument, planHash string) (string, error) {
	var approvalID string
	hash := strings.TrimSpace(planHash)
	if hash == "" {
		var err error
		hash, err = plan.Hash()
		if err != nil {
			return "", err
		}
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT approval_id FROM approvals
		WHERE plan_id = ? AND plan_revision = ? AND scope_hash = ? AND decision = ?
		AND scope_type = ? AND step_id IS NULL AND invalidated_at IS NULL
		ORDER BY decided_at DESC, approval_id DESC LIMIT 1`,
		plan.PlanID, plan.Revision, hash, approval.DecisionApproved, approval.ScopePlan,
	).Scan(&approvalID)
	if errors.Is(err, sql.ErrNoRows) {
		h2, herr := plan.Hash()
		if herr != nil {
			return "", herr
		}
		err = s.db.QueryRowContext(ctx, `
			SELECT approval_id FROM approvals
			WHERE plan_id = ? AND plan_revision = ? AND scope_hash = ? AND decision = ?
			AND scope_type = ? AND step_id IS NULL AND invalidated_at IS NULL
			ORDER BY decided_at DESC, approval_id DESC LIMIT 1`,
			plan.PlanID, plan.Revision, h2, approval.DecisionApproved, approval.ScopePlan,
		).Scan(&approvalID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", approval.ErrNotApproved
	}
	if err != nil {
		return "", err
	}
	return approvalID, nil
}

func shortHash(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			_, _ = h.Write([]byte{0})
		}
		_, _ = h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:24]
}
