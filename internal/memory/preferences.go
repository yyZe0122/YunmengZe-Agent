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

func (s *Store) Preferences(ctx context.Context, limit int, now string) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+entrySelectCols+` FROM memory_entries WHERE kind='preference' AND archived_at='' AND (expires_at='' OR expires_at>?) ORDER BY updated_at DESC,entry_id LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEntries(rows)
}
func (s *Store) savePreference(ctx context.Context, p preferenceProposal, stamp string, ttl time.Duration) error {
	key := p.Key
	if !strings.HasPrefix(key, p.Category+"/") {
		key = p.Category + "/" + key
	}
	var id, archived, content, source, updated, evidenceJSON string
	err := s.db.QueryRowContext(ctx, `SELECT entry_id,archived_at,content,source,updated_at,evidence_json FROM memory_entries WHERE preference_key=? OR (kind='preference' AND lower(content)=lower(?)) ORDER BY archived_at DESC LIMIT 1`, key, p.Content).Scan(&id, &archived, &content, &source, &updated, &evidenceJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if archived != "" || (id != "" && !p.Explicit) {
		return nil
	}
	if id != "" {
		var evidence []Evidence
		if err = json.Unmarshal([]byte(evidenceJSON), &evidence); err != nil {
			return err
		}
		for _, ev := range evidence {
			if ev.MessageID >= p.latestEvidenceID {
				return nil
			}
		}
		if source == SourceUser {
			changed, _ := time.Parse(time.RFC3339Nano, updated)
			observed, _ := time.Parse(time.RFC3339Nano, p.latestEvidenceAt)
			if !observed.After(changed) {
				return nil
			}
		}
	}
	if id == "" {
		id = "pref-" + shortID(key)
	}
	expires := ""
	if ttl > 0 {
		created, _ := time.Parse(time.RFC3339Nano, stamp)
		expires = created.Add(ttl).Format(time.RFC3339Nano)
	}
	evidence, _ := json.Marshal(p.Evidence)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO memory_entries(entry_id,session_id,content,source,tags_json,kind,priority,created_at,updated_at,preference_key,evidence_json,expires_at) VALUES(?,'',?,'curator','[]','preference',10,?,?,?,?,?) ON CONFLICT(entry_id) DO UPDATE SET content=excluded.content,source=excluded.source,evidence_json=excluded.evidence_json,updated_at=excluded.updated_at,expires_at=excluded.expires_at`, id, p.Content, stamp, stamp, key, string(evidence), expires)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM memory_entries_fts WHERE entry_id=?`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO memory_entries_fts(entry_id,content) VALUES(?,?)`, id, p.Content); err != nil {
		return err
	}
	return tx.Commit()
}

// Forget keeps a tombstone so automatic curation cannot restore this key.
func (m *Manager) Forget(ctx context.Context, id string) error {
	stamp := m.now().UTC().Format(time.RFC3339Nano)
	result, err := m.store.db.ExecContext(ctx, `UPDATE memory_entries SET archived_at=?,updated_at=? WHERE entry_id=? AND archived_at=''`, stamp, stamp, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory %q not found", id)
	}
	m.InvalidateSnapshot("")
	return nil
}
func (m *Manager) Correct(ctx context.Context, id, content string) error {
	content = strings.TrimSpace(content)
	if content == "" || len([]rune(content)) > 800 {
		return errors.New("preference must contain 1..800 characters")
	}
	tx, err := m.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE memory_entries SET content=?,source='user',kind='preference',session_id='',evidence_json='[]',updated_at=? WHERE entry_id=? AND archived_at=''`, content, m.now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory %q not found", id)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM memory_entries_fts WHERE entry_id=?`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO memory_entries_fts(entry_id,content) VALUES(?,?)`, id, content); err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		m.InvalidateSnapshot("")
	}
	return err
}

func (s *Store) curationEntries(ctx context.Context) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+entrySelectCols+` FROM memory_entries WHERE kind='preference' ORDER BY updated_at DESC LIMIT 32`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEntries(rows)
}
