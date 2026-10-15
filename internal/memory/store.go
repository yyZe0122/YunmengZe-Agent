// Package memory stores user preferences and legacy records in the shared core.db.
package memory

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
	SourceBuiltin = "builtin"
	SourceUser    = "user"
	SourceCurator = "curator"

	KindPreference = "preference"
	KindCurated    = "curated"
	KindSession    = "session"
	KindDetail     = "detail"

	DefaultMaxInjectRunes = 2_000
	DefaultListLimit      = 32

	entrySelectCols = `entry_id, session_id, content, source, tags_json, created_at,
		       COALESCE(kind, 'session'), COALESCE(priority, 0),
		       COALESCE(expires_at, ''), COALESCE(updated_at, ''), COALESCE(archived_at, ''), preference_key, evidence_json`
	entrySelectPrefixed = `e.entry_id, e.session_id, e.content, e.source, e.tags_json, e.created_at,
		       COALESCE(e.kind, 'session'), COALESCE(e.priority, 0),
		       COALESCE(e.expires_at, ''), COALESCE(e.updated_at, ''), COALESCE(e.archived_at, ''), e.preference_key, e.evidence_json`
)

// Entry is one durable preference or legacy memory record.
type Entry struct {
	Key        string     `json:"key,omitempty"`
	Evidence   []Evidence `json:"evidence,omitempty"`
	ID         string     `json:"entry_id"`
	SessionID  string     `json:"session_id,omitempty"` // empty = user/global
	Content    string     `json:"content"`
	Source     string     `json:"source"`
	Tags       []string   `json:"tags,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Priority   int        `json:"priority,omitempty"`
	ExpiresAt  string     `json:"expires_at,omitempty"`
	CreatedAt  string     `json:"created_at"`
	UpdatedAt  string     `json:"updated_at,omitempty"`
	ArchivedAt string     `json:"archived_at,omitempty"`
}

// Store persists memory_entries and FTS on core.db.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("memory store requires database")
	}
	return &Store{db: db}, nil
}

// Insert writes one entry and updates FTS. Empty content is rejected.
func (s *Store) Insert(ctx context.Context, e Entry) error {
	if s == nil || s.db == nil {
		return errors.New("memory store is nil")
	}
	e = normalizeEntry(e)
	if e.ID == "" || e.Content == "" {
		return errors.New("memory entry id and content are required")
	}
	tagsJSON, _ := json.Marshal(e.Tags)
	if e.Tags == nil {
		tagsJSON = []byte("[]")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin memory insert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO memory_entries (
			entry_id, session_id, content, source, tags_json, created_at,
			kind, priority, expires_at, updated_at, archived_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.SessionID, e.Content, e.Source, string(tagsJSON), e.CreatedAt,
		e.Kind, e.Priority, e.ExpiresAt, e.UpdatedAt, e.ArchivedAt,
	)
	if err != nil {
		return fmt.Errorf("insert memory entry: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_entries_fts(entry_id, content) VALUES (?, ?)`,
		e.ID, e.Content); err != nil {
		// FTS may be missing on pre-020 DBs during partial tests; ignore only if table missing.
		if !isMissingTable(err) {
			return fmt.Errorf("insert memory fts: %w", err)
		}
	}
	return tx.Commit()
}

// ListRecent returns newest entries for session (and global when includeGlobal).
// includeDetail=false excludes kind=detail (for inject). Expired rows are skipped when nowRFC is set.
func (s *Store) ListRecent(ctx context.Context, sessionID string, includeGlobal, includeDetail bool, limit int, nowRFC string) ([]Entry, error) {
	if limit <= 0 || limit > 200 {
		limit = DefaultListLimit
	}
	sessionID = strings.TrimSpace(sessionID)
	var (
		rows *sql.Rows
		err  error
	)
	// ORDER: priority DESC, then created_at DESC
	baseSelect := `
		SELECT ` + entrySelectCols + `
		FROM memory_entries WHERE `
	scopeSQL, scopeArgs := scopeClause(sessionID, includeGlobal)
	kindSQL := ""
	if !includeDetail {
		kindSQL = ` AND COALESCE(kind, 'session') != 'detail'`
	}
	expSQL, expArgs := activeClause(nowRFC)
	args := append(scopeArgs, expArgs...)
	args = append(args, limit)
	q := baseSelect + scopeSQL + kindSQL + expSQL +
		` ORDER BY priority DESC, created_at DESC LIMIT ?`
	rows, err = s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list memory: %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

// Search returns matching entries (FTS when available, else LIKE).
func (s *Store) Search(ctx context.Context, sessionID, query string, includeDetail bool, limit int, nowRFC string) ([]Entry, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return s.ListRecent(ctx, sessionID, true, includeDetail, limit, nowRFC)
	}
	if limit <= 0 || limit > 200 {
		limit = DefaultListLimit
	}
	// Prefer FTS.
	entries, err := s.searchFTS(ctx, sessionID, query, includeDetail, limit, nowRFC)
	if err == nil {
		return entries, nil
	}
	if !isMissingTable(err) && !isFTSError(err) {
		return nil, err
	}
	return s.searchLike(ctx, sessionID, query, includeDetail, limit, nowRFC)
}

func (s *Store) searchFTS(ctx context.Context, sessionID, query string, includeDetail bool, limit int, nowRFC string) ([]Entry, error) {
	sessionID = strings.TrimSpace(sessionID)
	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return s.ListRecent(ctx, sessionID, true, includeDetail, limit, nowRFC)
	}
	kindSQL := ""
	if !includeDetail {
		kindSQL = ` AND COALESCE(e.kind, 'session') != 'detail'`
	}
	expSQL, expArgs := activeClausePrefixed(nowRFC, "e.")
	scopeSQL := `(e.session_id = ? OR e.session_id = '')`
	args := []any{ftsQuery, sessionID}
	args = append(args, expArgs...)
	args = append(args, limit)
	q := `
		SELECT ` + entrySelectPrefixed + `
		FROM memory_entries_fts f
		JOIN memory_entries e ON e.entry_id = f.entry_id
		WHERE memory_entries_fts MATCH ? AND ` + scopeSQL + kindSQL + expSQL + `
		ORDER BY e.priority DESC, e.created_at DESC
		LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEntries(rows)
}

func (s *Store) searchLike(ctx context.Context, sessionID, query string, includeDetail bool, limit int, nowRFC string) ([]Entry, error) {
	pattern := "%" + escapeLike(query) + "%"
	sessionID = strings.TrimSpace(sessionID)
	kindSQL := ""
	if !includeDetail {
		kindSQL = ` AND COALESCE(kind, 'session') != 'detail'`
	}
	expSQL, expArgs := activeClause(nowRFC)
	args := []any{sessionID, pattern}
	args = append(args, expArgs...)
	args = append(args, limit)
	q := `
		SELECT ` + entrySelectCols + `
		FROM memory_entries
		WHERE (session_id = ? OR session_id = '') AND content LIKE ? ESCAPE '\'` + kindSQL + expSQL + `
		ORDER BY priority DESC, created_at DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("search memory: %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

// ArchiveExpired marks due entries archived (does not delete rows or FTS).
func (s *Store) ArchiveExpired(ctx context.Context, nowRFC string) (int64, error) {
	nowRFC = strings.TrimSpace(nowRFC)
	if nowRFC == "" {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE memory_entries
		SET archived_at = ?, updated_at = ?
		WHERE archived_at = '' AND expires_at != '' AND expires_at <= ?`,
		nowRFC, nowRFC, nowRFC)
	if err != nil {
		return 0, fmt.Errorf("archive expired memory: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// normalizeEntry fills defaults shared by direct and curator writes.
func normalizeEntry(e Entry) Entry {
	e.ID = strings.TrimSpace(e.ID)
	e.SessionID = strings.TrimSpace(e.SessionID)
	e.Content = strings.TrimSpace(e.Content)
	if e.Source == "" {
		e.Source = SourceBuiltin
	}
	if e.Kind == "" {
		if e.SessionID == "" {
			e.Kind = KindCurated
		} else {
			e.Kind = KindSession
		}
	}
	if e.CreatedAt == "" {
		e.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if e.UpdatedAt == "" {
		e.UpdatedAt = e.CreatedAt
	}
	e.ArchivedAt = strings.TrimSpace(e.ArchivedAt)
	return e
}

func scopeClause(sessionID string, includeGlobal bool) (string, []any) {
	if sessionID == "" {
		return `session_id = ''`, nil
	}
	if includeGlobal {
		return `(session_id = ? OR session_id = '')`, []any{sessionID}
	}
	return `session_id = ?`, []any{sessionID}
}

func activeClause(nowRFC string) (string, []any) {
	nowRFC = strings.TrimSpace(nowRFC)
	if nowRFC == "" {
		return ` AND archived_at = ''`, nil
	}
	return ` AND archived_at = '' AND (expires_at = '' OR expires_at > ?)`, []any{nowRFC}
}

func activeClausePrefixed(nowRFC, prefix string) (string, []any) {
	nowRFC = strings.TrimSpace(nowRFC)
	if nowRFC == "" {
		return ` AND ` + prefix + `archived_at = ''`, nil
	}
	return ` AND ` + prefix + `archived_at = '' AND (` + prefix + `expires_at = '' OR ` + prefix + `expires_at > ?)`, []any{nowRFC}
}

type scannable interface {
	Scan(dest ...any) error
}

func scanEntry(row scannable) (Entry, error) {
	var e Entry
	var tagsJSON, evidenceJSON string
	if err := row.Scan(
		&e.ID, &e.SessionID, &e.Content, &e.Source, &tagsJSON, &e.CreatedAt,
		&e.Kind, &e.Priority, &e.ExpiresAt, &e.UpdatedAt, &e.ArchivedAt, &e.Key, &evidenceJSON,
	); err != nil {
		return Entry{}, err
	}
	_ = json.Unmarshal([]byte(tagsJSON), &e.Tags)
	_ = json.Unmarshal([]byte(evidenceJSON), &e.Evidence)
	if e.Tags == nil {
		e.Tags = []string{}
	}
	return e, nil
}

func scanEntries(rows *sql.Rows) ([]Entry, error) {
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// buildFTSQuery turns free text into a safe FTS5 query (AND of quoted tokens).
func buildFTSQuery(q string) string {
	parts := strings.Fields(q)
	if len(parts) == 0 {
		return ""
	}
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Strip FTS operators that could break MATCH.
		p = strings.Map(func(r rune) rune {
			switch r {
			case '"', '*', '(', ')', ':', '^':
				return -1
			default:
				return r
			}
		}, p)
		if p == "" {
			continue
		}
		out = append(out, `"`+p+`"`)
	}
	return strings.Join(out, " ")
}

func isMissingTable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "no such module")
}

func isFTSError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "fts5") || strings.Contains(msg, "sql logic error") ||
		strings.Contains(msg, "malformed") || strings.Contains(msg, "no such column")
}
