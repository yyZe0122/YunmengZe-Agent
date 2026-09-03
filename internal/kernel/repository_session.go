package kernel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (r *Repository) CreateSession(ctx context.Context, id SessionID, now time.Time) (Session, error) {
	return r.CreateSessionWithWorkspace(ctx, id, "", "", now)
}

// CreateSessionWithWorkspace creates a session and stores workspace + permission stance in metadata.
func (r *Repository) CreateSessionWithWorkspace(ctx context.Context, id SessionID, workspace, stance string, now time.Time) (Session, error) {
	if ctx == nil {
		return Session{}, errors.New("create session context is required")
	}
	session, err := NewSession(id, now)
	if err != nil {
		return Session{}, err
	}
	workspace = strings.TrimSpace(workspace)
	normalized, err := NormalizePermissionStance(stance)
	if err != nil {
		return Session{}, err
	}
	session.Workspace = workspace
	session.PermissionStance = normalized
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin create session: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
        INSERT INTO sessions (
            session_id, state, created_at, updated_at, metadata, version
        ) VALUES (?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.State,
		formatTime(session.CreatedAt),
		formatTime(session.UpdatedAt),
		sessionMetadataEncode(workspace, "", normalized, nil, nil),
		session.Version,
	)
	if err != nil {
		if existsByID(ctx, tx, "sessions", "session_id", string(session.ID)) {
			return Session{}, fmt.Errorf("%w: session %s", ErrAlreadyExists, session.ID)
		}
		return Session{}, fmt.Errorf("insert session: %w", err)
	}
	details := map[string]any{"state": session.State}
	if workspace != "" {
		details["workspace"] = workspace
	}
	ev, err := aggregateEvent(
		"session", string(session.ID), session.Version, "session.created", session.CreatedAt,
		details,
	)
	if err != nil {
		return Session{}, err
	}
	if _, err := r.events.AppendTx(ctx, tx, ev); err != nil {
		return Session{}, fmt.Errorf("append session event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit create session: %w", err)
	}
	return session, nil
}

// EnsureSessionWorkspace sets metadata.workspace when empty (first bind wins).
func (r *Repository) EnsureSessionWorkspace(ctx context.Context, id SessionID, workspace string) error {
	if ctx == nil {
		return errors.New("ensure session workspace context is required")
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil
	}
	session, err := r.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(session.Workspace) != "" {
		return nil
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE sessions SET metadata = ?, updated_at = ?
		WHERE session_id = ?`,
		sessionMetadataEncode(workspace, session.PreferredModel, session.PermissionStance, session.HiddenTaskIDs, session.ExtraRoots), formatTime(time.Now().UTC()), id,
	)
	if err != nil {
		return fmt.Errorf("set session workspace: %w", err)
	}
	return nil
}

func (r *Repository) GetSession(ctx context.Context, id SessionID) (Session, error) {
	if ctx == nil {
		return Session{}, errors.New("get session context is required")
	}
	return scanSession(r.db.QueryRowContext(ctx, `
        SELECT session_id, state, version, created_at, updated_at, metadata
        FROM sessions WHERE session_id = ?`, id))
}

func (r *Repository) CloseSession(ctx context.Context, id SessionID, expectedVersion uint64, now time.Time) (Session, error) {
	if ctx == nil {
		return Session{}, errors.New("close session context is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin close session: %w", err)
	}
	defer tx.Rollback()

	session, err := scanSession(tx.QueryRowContext(ctx, `
        SELECT session_id, state, version, created_at, updated_at, metadata
        FROM sessions WHERE session_id = ?`, id))
	if err != nil {
		return Session{}, err
	}
	if session.Version != expectedVersion {
		return Session{}, versionConflict("session", string(id), expectedVersion, session.Version)
	}
	previous := session.State
	if err := session.Close(now); err != nil {
		return Session{}, err
	}
	result, err := tx.ExecContext(ctx, `
        UPDATE sessions SET state = ?, version = ?, updated_at = ?
        WHERE session_id = ? AND version = ?`,
		session.State, session.Version, formatTime(session.UpdatedAt), session.ID, expectedVersion,
	)
	if err != nil {
		return Session{}, fmt.Errorf("update session: %w", err)
	}
	if err := requireOneVersionedRow(result, "session", string(id), expectedVersion); err != nil {
		return Session{}, err
	}
	ev, err := aggregateEvent(
		"session", string(session.ID), session.Version, "session.state_changed", session.UpdatedAt,
		map[string]any{"from_state": previous, "to_state": session.State},
	)
	if err != nil {
		return Session{}, err
	}
	if _, err := r.events.AppendTx(ctx, tx, ev); err != nil {
		return Session{}, fmt.Errorf("append session transition event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit close session: %w", err)
	}
	return session, nil
}

func scanSession(row scanner) (Session, error) {
	var session Session
	var id, state, createdAt, updatedAt, metadata string
	var version int64
	if err := row.Scan(&id, &state, &version, &createdAt, &updatedAt, &metadata); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("scan session: %w", err)
	}
	created, err := parseTime(createdAt)
	if err != nil {
		return Session{}, err
	}
	updated, err := parseTime(updatedAt)
	if err != nil {
		return Session{}, err
	}
	session.ID = SessionID(id)
	session.State = SessionState(state)
	session.Version = uint64(version)
	session.CreatedAt = created
	session.UpdatedAt = updated
	session.Workspace = workspaceFromMetadata(metadata)
	session.PreferredModel = preferredModelFromMetadata(metadata)
	session.PermissionStance = permissionStanceFromMetadata(metadata)
	session.HiddenTaskIDs = hiddenTaskIDsFromMetadata(metadata)
	session.ExtraRoots = extraRootsFromMetadata(metadata)
	return session, nil
}

func sessionMetadataEncode(workspace, preferredModel, permissionStance string, hidden, extraRoots []string) string {
	workspace = strings.TrimSpace(workspace)
	preferredModel = strings.TrimSpace(preferredModel)
	permissionStance = strings.TrimSpace(permissionStance)
	if permissionStance == PermissionStanceAgent {
		permissionStance = ""
	}
	meta := map[string]any{}
	if workspace != "" {
		meta["workspace"] = workspace
	}
	if preferredModel != "" {
		meta["model"] = preferredModel
	}
	if permissionStance != "" {
		meta["permission_stance"] = permissionStance
	}
	if ids := normalizeHiddenTaskIDs(hidden); len(ids) > 0 {
		meta["hidden_task_ids"] = ids
	}
	if roots := normalizeHiddenTaskIDs(extraRoots); len(roots) > 0 {
		meta["extra_roots"] = roots
	}
	if len(meta) == 0 {
		return "{}"
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func workspaceFromMetadata(raw string) string {
	return metaStringField(raw, "workspace")
}

func preferredModelFromMetadata(raw string) string {
	return metaStringField(raw, "model")
}

func permissionStanceFromMetadata(raw string) string {
	stance := metaStringField(raw, "permission_stance")
	normalized, err := NormalizePermissionStance(stance)
	if err != nil {
		return PermissionStanceAgent
	}
	return normalized
}

func extraRootsFromMetadata(raw string) []string {
	return metaStringSlice(raw, "extra_roots")
}

func hiddenTaskIDsFromMetadata(raw string) []string {
	return metaStringSlice(raw, "hidden_task_ids")
}

func metaStringSlice(raw, key string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return nil
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return nil
	}
	v, ok := meta[key]
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				continue
			}
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return normalizeHiddenTaskIDs(out)
	case []string:
		return normalizeHiddenTaskIDs(t)
	default:
		return nil
	}
}

func normalizeHiddenTaskIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func metaStringField(raw, key string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return ""
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return ""
	}
	if v, ok := meta[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// SetSessionPreferredModel merges preferred model into session metadata (preserves workspace).
func (r *Repository) SetSessionPreferredModel(ctx context.Context, id SessionID, model string) error {
	if ctx == nil {
		return errors.New("set session preferred model context is required")
	}
	model = strings.TrimSpace(model)
	if model != "" {
		providerID, modelID, ok := strings.Cut(model, "/")
		providerID, modelID = strings.TrimSpace(providerID), strings.TrimSpace(modelID)
		if !ok || providerID == "" || modelID == "" {
			return fmt.Errorf("%w: preferred model must use provider/model format", ErrInvalidAggregate)
		}
		model = providerID + "/" + modelID
	}
	session, err := r.GetSession(ctx, id)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE sessions SET metadata = ?, updated_at = ?
		WHERE session_id = ?`,
		sessionMetadataEncode(session.Workspace, model, session.PermissionStance, session.HiddenTaskIDs, session.ExtraRoots), formatTime(time.Now().UTC()), id,
	)
	if err != nil {
		return fmt.Errorf("set session preferred model: %w", err)
	}
	return nil
}

func (r *Repository) SetSessionPermissionStance(ctx context.Context, id SessionID, stance string) error {
	if ctx == nil {
		return errors.New("set session permission stance context is required")
	}
	normalized, err := NormalizePermissionStance(stance)
	if err != nil {
		return err
	}
	session, err := r.GetSession(ctx, id)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE sessions SET metadata = ?, updated_at = ?
		WHERE session_id = ?`,
		sessionMetadataEncode(session.Workspace, session.PreferredModel, normalized, session.HiddenTaskIDs, session.ExtraRoots), formatTime(time.Now().UTC()), id,
	)
	if err != nil {
		return fmt.Errorf("set session permission stance: %w", err)
	}
	return nil
}

func (r *Repository) SetSessionHiddenTaskIDs(ctx context.Context, id SessionID, hidden []string) error {
	if ctx == nil {
		return errors.New("set session hidden tasks context is required")
	}
	session, err := r.GetSession(ctx, id)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE sessions SET metadata = ?, updated_at = ?
		WHERE session_id = ?`,
		sessionMetadataEncode(session.Workspace, session.PreferredModel, session.PermissionStance, hidden, session.ExtraRoots), formatTime(time.Now().UTC()), id,
	)
	if err != nil {
		return fmt.Errorf("set session hidden tasks: %w", err)
	}
	return nil
}

func (r *Repository) SetSessionExtraRoots(ctx context.Context, id SessionID, extra []string) error {
	if ctx == nil {
		return errors.New("set session extra roots context is required")
	}
	session, err := r.GetSession(ctx, id)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE sessions SET metadata = ?, updated_at = ?
		WHERE session_id = ?`,
		sessionMetadataEncode(session.Workspace, session.PreferredModel, session.PermissionStance, session.HiddenTaskIDs, extra), formatTime(time.Now().UTC()), id,
	)
	if err != nil {
		return fmt.Errorf("set session extra roots: %w", err)
	}
	return nil
}
