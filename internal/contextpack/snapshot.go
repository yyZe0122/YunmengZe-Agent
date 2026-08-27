package contextpack

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SnapshotSource labels how last_prompt_tokens was obtained.
const (
	SourceNone          = "none"
	SourceProviderUsage = "provider_usage"
	SourceLocalEstimate = "local_estimate"
)

// Snapshot is a task-scoped context pressure record (monitoring + packing hint).
type Snapshot struct {
	TaskID           string  `json:"task_id"`
	SessionID        string  `json:"session_id"`
	Model            string  `json:"model"`
	ContextWindow    int64   `json:"context_window"`
	MaxOutputTokens  int64   `json:"max_output_tokens"`
	UsableTokens     int64   `json:"usable_tokens"`
	LastPromptTokens int64   `json:"last_prompt_tokens"`
	LastOutputTokens int64   `json:"last_output_tokens"`
	EstimateTokens   int64   `json:"estimate_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	Source           string  `json:"source"`
	EstimateSource   string  `json:"estimate_source"`
	Ratio            float64 `json:"ratio"`
	Calibrated       bool    `json:"calibrated"`
	Compacted        bool    `json:"compacted"`
	HistoryMessages  int     `json:"history_messages"`
	Pressure         float64 `json:"pressure"`
	UpdatedAt        string  `json:"updated_at"`
}

// PressureOf returns last_prompt/usable (or estimate if no prompt).
func (s Snapshot) PressureOf() float64 {
	if s.UsableTokens <= 0 {
		return 0
	}
	fill := s.LastPromptTokens
	if fill <= 0 {
		fill = s.EstimateTokens
	}
	if fill <= 0 {
		return 0
	}
	return float64(fill) / float64(s.UsableTokens)
}

// Store persists context snapshots on core.db.
type Store struct {
	db *sql.DB
}

// NewStore wraps the shared core connection.
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("contextpack store requires db")
	}
	return &Store{db: db}, nil
}

// Upsert writes or replaces the task snapshot.
func (s *Store) Upsert(ctx context.Context, snap Snapshot) error {
	if s == nil {
		return errors.New("contextpack store is nil")
	}
	taskID := strings.TrimSpace(snap.TaskID)
	if taskID == "" {
		return errors.New("task_id is required")
	}
	sessionID := strings.TrimSpace(snap.SessionID)
	if sessionID == "" {
		return errors.New("session_id is required")
	}
	if snap.Source == "" {
		snap.Source = SourceNone
	}
	if snap.EstimateSource == "" {
		snap.EstimateSource = SourceLocalEstimate
	}
	if snap.Ratio <= 0 {
		snap.Ratio = 1
	}
	updated := strings.TrimSpace(snap.UpdatedAt)
	if updated == "" {
		updated = time.Now().UTC().Format(time.RFC3339Nano)
	}
	calibrated := 0
	if snap.Calibrated {
		calibrated = 1
	}
	compacted := 0
	if snap.Compacted {
		compacted = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO context_snapshots (
			task_id, session_id, model, context_window, max_output_tokens, usable_tokens,
			last_prompt_tokens, last_output_tokens, estimate_tokens,
			cache_read_tokens, cache_write_tokens, source, estimate_source,
			ratio, calibrated, compacted, history_messages, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET
			session_id = excluded.session_id,
			model = excluded.model,
			context_window = excluded.context_window,
			max_output_tokens = excluded.max_output_tokens,
			usable_tokens = excluded.usable_tokens,
			last_prompt_tokens = excluded.last_prompt_tokens,
			last_output_tokens = excluded.last_output_tokens,
			estimate_tokens = excluded.estimate_tokens,
			cache_read_tokens = excluded.cache_read_tokens,
			cache_write_tokens = excluded.cache_write_tokens,
			source = excluded.source,
			estimate_source = excluded.estimate_source,
			ratio = excluded.ratio,
			calibrated = excluded.calibrated,
			compacted = excluded.compacted,
			history_messages = excluded.history_messages,
			updated_at = excluded.updated_at`,
		taskID, sessionID, snap.Model, snap.ContextWindow, snap.MaxOutputTokens, snap.UsableTokens,
		snap.LastPromptTokens, snap.LastOutputTokens, snap.EstimateTokens,
		snap.CacheReadTokens, snap.CacheWriteTokens, snap.Source, snap.EstimateSource,
		snap.Ratio, calibrated, compacted, snap.HistoryMessages, updated,
	)
	if err != nil {
		return fmt.Errorf("upsert context snapshot: %w", err)
	}
	return nil
}

// GetByTask returns the snapshot for taskID.
func (s *Store) GetByTask(ctx context.Context, taskID string) (Snapshot, error) {
	if s == nil {
		return Snapshot{}, errors.New("contextpack store is nil")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return Snapshot{}, errors.New("task_id is required")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT task_id, session_id, model, context_window, max_output_tokens, usable_tokens,
			last_prompt_tokens, last_output_tokens, estimate_tokens,
			cache_read_tokens, cache_write_tokens, source, estimate_source,
			ratio, calibrated, compacted, history_messages, updated_at
		FROM context_snapshots WHERE task_id = ?`, taskID)
	return scanSnapshot(row)
}

// GetBySession returns the most recently updated snapshot for sessionID.
func (s *Store) GetBySession(ctx context.Context, sessionID string) (Snapshot, error) {
	if s == nil {
		return Snapshot{}, errors.New("contextpack store is nil")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Snapshot{}, errors.New("session_id is required")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT task_id, session_id, model, context_window, max_output_tokens, usable_tokens,
			last_prompt_tokens, last_output_tokens, estimate_tokens,
			cache_read_tokens, cache_write_tokens, source, estimate_source,
			ratio, calibrated, compacted, history_messages, updated_at
		FROM context_snapshots WHERE session_id = ?
		ORDER BY updated_at DESC LIMIT 1`, sessionID)
	return scanSnapshot(row)
}

func scanSnapshot(row *sql.Row) (Snapshot, error) {
	var snap Snapshot
	var calibrated, compacted int
	err := row.Scan(
		&snap.TaskID, &snap.SessionID, &snap.Model, &snap.ContextWindow, &snap.MaxOutputTokens, &snap.UsableTokens,
		&snap.LastPromptTokens, &snap.LastOutputTokens, &snap.EstimateTokens,
		&snap.CacheReadTokens, &snap.CacheWriteTokens, &snap.Source, &snap.EstimateSource,
		&snap.Ratio, &calibrated, &compacted, &snap.HistoryMessages, &snap.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, sql.ErrNoRows
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("scan context snapshot: %w", err)
	}
	snap.Calibrated = calibrated != 0
	snap.Compacted = compacted != 0
	snap.Pressure = snap.PressureOf()
	return snap, nil
}

// Compaction is a durable session summary for provider-view head replacement.
type Compaction struct {
	ID               string
	SessionID        string
	Summary          string
	ThroughMessageID string
	Model            string
	CreatedAt        string
}

// InsertCompaction stores a new session summary.
func (s *Store) InsertCompaction(ctx context.Context, c Compaction) error {
	if s == nil {
		return errors.New("contextpack store is nil")
	}
	if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.SessionID) == "" || strings.TrimSpace(c.Summary) == "" {
		return errors.New("compaction id, session_id, and summary are required")
	}
	created := strings.TrimSpace(c.CreatedAt)
	if created == "" {
		created = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO session_compactions (compaction_id, session_id, summary, through_message_id, model, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, c.SessionID, c.Summary, c.ThroughMessageID, c.Model, created,
	)
	if err != nil {
		return fmt.Errorf("insert session compaction: %w", err)
	}
	return nil
}

// DeleteCompaction removes one durable session head summary.
func (s *Store) DeleteCompaction(ctx context.Context, compactionID string) error {
	if s == nil {
		return errors.New("contextpack store is nil")
	}
	compactionID = strings.TrimSpace(compactionID)
	if compactionID == "" {
		return errors.New("compaction id is required")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM session_compactions WHERE compaction_id = ?`, compactionID)
	if err != nil {
		return fmt.Errorf("delete session compaction: %w", err)
	}
	return nil
}

// LatestCompaction returns the newest summary for sessionID, or sql.ErrNoRows.
func (s *Store) LatestCompaction(ctx context.Context, sessionID string) (Compaction, error) {
	if s == nil {
		return Compaction{}, errors.New("contextpack store is nil")
	}
	sessionID = strings.TrimSpace(sessionID)
	var c Compaction
	err := s.db.QueryRowContext(ctx, `
		SELECT compaction_id, session_id, summary, through_message_id, model, created_at
		FROM session_compactions WHERE session_id = ?
		ORDER BY created_at DESC LIMIT 1`, sessionID,
	).Scan(&c.ID, &c.SessionID, &c.Summary, &c.ThroughMessageID, &c.Model, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Compaction{}, sql.ErrNoRows
	}
	if err != nil {
		return Compaction{}, fmt.Errorf("latest compaction: %w", err)
	}
	return c, nil
}

// CountCompactionsSince returns how many durable compactions exist for sessionID
// with created_at >= sinceUTC (RFC3339Nano comparable strings).
func (s *Store) CountCompactionsSince(ctx context.Context, sessionID, sinceUTC string) (int, error) {
	if s == nil {
		return 0, errors.New("contextpack store is nil")
	}
	sessionID = strings.TrimSpace(sessionID)
	sinceUTC = strings.TrimSpace(sinceUTC)
	if sessionID == "" || sinceUTC == "" {
		return 0, errors.New("session_id and since are required")
	}
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM session_compactions
		WHERE session_id = ? AND created_at >= ?`, sessionID, sinceUTC,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count session compactions: %w", err)
	}
	return n, nil
}

// AllowLLMCompact reports whether another LLM head summary is allowed under
// anti-thrash limits. Fail-open (allow) when the store or query fails so packing
// still works; callers may still fall back to extractive.
func (s *Store) AllowLLMCompact(ctx context.Context, sessionID string, now time.Time, window time.Duration, max int) bool {
	if s == nil {
		return true
	}
	if max <= 0 {
		max = DefaultAntiThrashMax
	}
	if window <= 0 {
		window = DefaultAntiThrashWindow
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	since := now.UTC().Add(-window).Format(time.RFC3339Nano)
	n, err := s.CountCompactionsSince(ctx, sessionID, since)
	if err != nil {
		return true
	}
	return n < max
}
