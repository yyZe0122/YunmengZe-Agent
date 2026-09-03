// Package toolpermission implements Crush-style tool-call permission (ADR-043).
package toolpermission

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	StatePending          = "pending"
	StateAllowedOnce      = "allowed_once"
	StateAllowedSimilar   = "allowed_similar"
	StateAllowedPermanent = "allowed_permanent"
	StateDenied           = "denied"
	StateExpired          = "expired"
	StateCancelled        = "cancelled"

	DecisionAllowOnce      = "allow_once"
	DecisionAllowSimilar   = "allow_similar"
	DecisionAllowPermanent = "allow_permanent"
	DecisionDeny           = "deny"
)

var (
	ErrNotFound      = errors.New("permission request not found")
	ErrNotPending    = errors.New("permission request is not pending")
	ErrInvalidDecide = errors.New("invalid permission decision")
)

// Request is a durable tool-call permission row.
type Request struct {
	ID            string          `json:"permission_id"`
	SessionID     string          `json:"session_id"`
	TaskID        string          `json:"task_id"`
	RunID         string          `json:"run_id"`
	PlanID        string          `json:"plan_id"`
	PlanHash      string          `json:"plan_hash"`
	StepID        string          `json:"step_id"`
	ToolCallID    string          `json:"tool_call_id"`
	ToolName      string          `json:"tool_name"`
	Arguments     json.RawMessage `json:"arguments"`
	Capability    string          `json:"capability"`
	Path          string          `json:"path,omitempty"`
	Command       string          `json:"command,omitempty"`
	CommandArgs   []string        `json:"command_args,omitempty"`
	NetworkDomain string          `json:"network_domain,omitempty"`
	Risk          string          `json:"risk,omitempty"`
	State         string          `json:"state"`
	GrantID       string          `json:"grant_id,omitempty"`
	Decision      string          `json:"decision,omitempty"`
	DecidedBy     string          `json:"decided_by,omitempty"`
	CreatedAt     string          `json:"created_at"`
	DecidedAt     string          `json:"decided_at,omitempty"`
	ExpiresAt     string          `json:"expires_at,omitempty"`
	// ExtraRoot is set on CreatePending for interactive extra-root /perm (not persisted).
	ExtraRoot bool `json:"extra_root,omitempty"`
}

// Store persists permission requests on core.db.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("toolpermission store requires database")
	}
	return &Store{db: db}, nil
}

// Insert creates a pending permission request.
func (s *Store) Insert(ctx context.Context, r Request) error {
	if s == nil || s.db == nil {
		return errors.New("toolpermission store is nil")
	}
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.ToolCallID) == "" || strings.TrimSpace(r.ToolName) == "" {
		return errors.New("permission id, tool_call_id, and tool_name are required")
	}
	if r.State == "" {
		r.State = StatePending
	}
	argsJSON, _ := json.Marshal(r.CommandArgs)
	if r.CommandArgs == nil {
		argsJSON = []byte("[]")
	}
	argBody := strings.TrimSpace(string(r.Arguments))
	if argBody == "" {
		argBody = "{}"
	}
	created := strings.TrimSpace(r.CreatedAt)
	if created == "" {
		created = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tool_permission_requests (
			permission_id, session_id, task_id, run_id, plan_id, plan_hash, step_id,
			tool_call_id, tool_name, arguments_json, capability, path, command_name,
			command_args_json, network_domain, risk, state, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.SessionID, r.TaskID, r.RunID, r.PlanID, r.PlanHash, r.StepID,
		r.ToolCallID, r.ToolName, argBody, r.Capability, r.Path, r.Command,
		string(argsJSON), r.NetworkDomain, r.Risk, r.State, created, nullStr(r.ExpiresAt),
	)
	if err != nil {
		return fmt.Errorf("insert tool permission: %w", err)
	}
	return nil
}

// Get returns one permission by id.
func (s *Store) Get(ctx context.Context, id string) (Request, error) {
	return s.scanOne(ctx, `
		SELECT permission_id, session_id, task_id, run_id, plan_id, plan_hash, step_id,
			tool_call_id, tool_name, arguments_json, capability, path, command_name,
			command_args_json, network_domain, risk, state, grant_id, decision, decided_by,
			created_at, decided_at, expires_at
		FROM tool_permission_requests WHERE permission_id = ?`, strings.TrimSpace(id))
}

// ListPending returns pending permissions, optionally filtered by session.
func (s *Store) ListPending(ctx context.Context, sessionID string, limit int) ([]Request, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	sessionID = strings.TrimSpace(sessionID)
	var rows *sql.Rows
	var err error
	if sessionID == "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT permission_id, session_id, task_id, run_id, plan_id, plan_hash, step_id,
				tool_call_id, tool_name, arguments_json, capability, path, command_name,
				command_args_json, network_domain, risk, state, grant_id, decision, decided_by,
				created_at, decided_at, expires_at
			FROM tool_permission_requests WHERE state = ?
			ORDER BY created_at ASC LIMIT ?`, StatePending, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT permission_id, session_id, task_id, run_id, plan_id, plan_hash, step_id,
				tool_call_id, tool_name, arguments_json, capability, path, command_name,
				command_args_json, network_domain, risk, state, grant_id, decision, decided_by,
				created_at, decided_at, expires_at
			FROM tool_permission_requests WHERE state = ? AND session_id = ?
			ORDER BY created_at ASC LIMIT ?`, StatePending, sessionID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list pending permissions: %w", err)
	}
	defer rows.Close()
	var out []Request
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkDecided updates a pending row with the decision outcome.
func (s *Store) MarkDecided(ctx context.Context, id, decision, state, grantID, decidedBy, decidedAt string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("permission id is required")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE tool_permission_requests
		SET state = ?, decision = ?, grant_id = ?, decided_by = ?, decided_at = ?
		WHERE permission_id = ? AND state = ?`,
		state, decision, nullStr(grantID), decidedBy, decidedAt, id, StatePending,
	)
	if err != nil {
		return fmt.Errorf("mark permission decided: %w", err)
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrNotPending
	}
	return nil
}

func (s *Store) scanOne(ctx context.Context, query string, arg string) (Request, error) {
	row := s.db.QueryRowContext(ctx, query, arg)
	r, err := scanRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	return r, err
}

type scannable interface {
	Scan(dest ...any) error
}

func scanRequest(row scannable) (Request, error) {
	var r Request
	var argsJSON, cmdArgsJSON string
	var grantID, decision, decidedBy, decidedAt, expiresAt sql.NullString
	err := row.Scan(
		&r.ID, &r.SessionID, &r.TaskID, &r.RunID, &r.PlanID, &r.PlanHash, &r.StepID,
		&r.ToolCallID, &r.ToolName, &argsJSON, &r.Capability, &r.Path, &r.Command,
		&cmdArgsJSON, &r.NetworkDomain, &r.Risk, &r.State, &grantID, &decision, &decidedBy,
		&r.CreatedAt, &decidedAt, &expiresAt,
	)
	if err != nil {
		return Request{}, err
	}
	r.Arguments = json.RawMessage(argsJSON)
	_ = json.Unmarshal([]byte(cmdArgsJSON), &r.CommandArgs)
	if r.CommandArgs == nil {
		r.CommandArgs = []string{}
	}
	if grantID.Valid {
		r.GrantID = grantID.String
	}
	if decision.Valid {
		r.Decision = decision.String
	}
	if decidedBy.Valid {
		r.DecidedBy = decidedBy.String
	}
	if decidedAt.Valid {
		r.DecidedAt = decidedAt.String
	}
	if expiresAt.Valid {
		r.ExpiresAt = expiresAt.String
	}
	return r, nil
}

// RecentDecision is a prior decide used for habit hints (H4).
type RecentDecision struct {
	Decision string
	Path     string
}

// ListRecentDecisions returns latest decided rows for tool+capability.
// Non-empty sessionID limits the query to that session.
func (s *Store) ListRecentDecisions(ctx context.Context, toolName, capability, sessionID string, limit int) ([]RecentDecision, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("toolpermission store is nil")
	}
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	query := `
		SELECT COALESCE(decision, ''), COALESCE(path, '')
		FROM tool_permission_requests
		WHERE tool_name = ? AND capability = ? AND state != ? AND COALESCE(decision, '') != ''`
	args := []any{strings.TrimSpace(toolName), strings.TrimSpace(capability), StatePending}
	if sid := strings.TrimSpace(sessionID); sid != "" {
		query += ` AND session_id = ?`
		args = append(args, sid)
	}
	query += ` ORDER BY COALESCE(decided_at, created_at) DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecentDecision
	for rows.Next() {
		var d RecentDecision
		if err := rows.Scan(&d.Decision, &d.Path); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
