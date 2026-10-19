// Package memory stores user preferences and legacy records in the shared core.db.
package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	SourceUser  = "user"
	KindCurated = "curated"

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

// ListRecent returns newest entries for session (and global when includeGlobal).
// includeDetail=false excludes legacy kind=detail records. Expired rows are skipped when nowRFC is set.
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

// Search returns matching entries from the migrated FTS index.
func (s *Store) Search(ctx context.Context, sessionID, query string, includeDetail bool, limit int, nowRFC string) ([]Entry, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return s.ListRecent(ctx, sessionID, true, includeDetail, limit, nowRFC)
	}
	if limit <= 0 || limit > 200 {
		limit = DefaultListLimit
	}
	return s.searchFTS(ctx, sessionID, query, includeDetail, limit, nowRFC)
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

// ArchiveExpired archives legacy entries. Preferences expire by expires_at;
// archived_at remains a tombstone, including archives from older versions.
func (s *Store) ArchiveExpired(ctx context.Context, nowRFC string) (int64, error) {
	nowRFC = strings.TrimSpace(nowRFC)
	if nowRFC == "" {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE memory_entries
		SET archived_at = ?, updated_at = ?
		WHERE kind != 'preference' AND archived_at = '' AND expires_at != '' AND expires_at <= ?`,
		nowRFC, nowRFC, nowRFC)
	if err != nil {
		return 0, fmt.Errorf("archive expired memory: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
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
