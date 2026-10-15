// Package session persists conversations in the single core.db.
package session

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

type Mode string

const (
	Agent Mode = "agent"
	Plan  Mode = "plan"
)

func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "agent":
		return Agent, nil
	case "plan":
		return Plan, nil
	}
	return "", fmt.Errorf("mode must be plan or agent")
}

type Session struct {
	SummaryReplay  *providerapi.Replay `json:"summary_replay,omitempty"`
	ID             string              `json:"id"`
	ParentID       string              `json:"parent_id,omitempty"`
	Kind           string              `json:"kind"`
	Title          string              `json:"title"`
	Workspace      string              `json:"workspace"`
	Mode           Mode                `json:"mode"`
	Model          string              `json:"model"`
	Summary        string              `json:"summary,omitempty"`
	SummaryThrough int64               `json:"summary_through,omitempty"`
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
}
type Record struct {
	ID        int64               `json:"id"`
	Message   providerapi.Message `json:"message"`
	Usage     providerapi.Usage   `json:"usage"`
	CreatedAt string              `json:"created_at"`
}
type Store struct{ db *sql.DB }

var ErrBusy = errors.New("session is already running")

func New(db *sql.DB) *Store { return &Store{db: db} }
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

const columns = `session_id,parent_id,kind,title,workspace,mode,model,summary,summary_through,created_at,updated_at,summary_replay`

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Session, error) {
	var s Session
	var replay string
	err := row.Scan(&s.ID, &s.ParentID, &s.Kind, &s.Title, &s.Workspace, &s.Mode, &s.Model, &s.Summary, &s.SummaryThrough, &s.CreatedAt, &s.UpdatedAt, &replay)
	if err == nil && replay != "" {
		err = json.Unmarshal([]byte(replay), &s.SummaryReplay)
	}
	return s, err
}
func (s *Store) Create(ctx context.Context, item Session) (Session, error) {
	var err error
	item.Mode, err = ParseMode(string(item.Mode))
	if err != nil {
		return item, err
	}
	if item.Workspace == "" {
		return item, errors.New("session workspace is required")
	}
	item.ID = NewID()
	item.CreatedAt = now()
	item.UpdatedAt = item.CreatedAt
	if item.Kind == "" {
		item.Kind = "general"
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO chat_sessions(session_id,parent_id,kind,title,workspace,mode,model,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, item.ID, item.ParentID, item.Kind, item.Title, item.Workspace, item.Mode, item.Model, item.CreatedAt, item.UpdatedAt)
	return item, err
}
func (s *Store) Get(ctx context.Context, id string) (Session, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM chat_sessions WHERE session_id=?`, id))
}
func (s *Store) List(ctx context.Context, workspace string) ([]Session, error) {
	query := `SELECT ` + columns + ` FROM chat_sessions WHERE parent_id=''`
	var args []any
	if workspace != "" {
		query += ` AND workspace=?`
		args = append(args, workspace)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY updated_at DESC,session_id LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Store) Configure(ctx context.Context, id string, mode Mode, model string) error {
	normalized, err := ParseMode(string(mode))
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE chat_sessions SET mode=?,model=?,updated_at=? WHERE session_id=? AND (lease_owner='' OR lease_until<?)`, normalized, model, now(), id, now())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrBusy
	}
	return err
}
func (s *Store) Append(ctx context.Context, id string, message providerapi.Message, usage providerapi.Usage) (int64, error) {
	raw, err := json.Marshal(message)
	if err != nil {
		return 0, err
	}
	u, err := json.Marshal(usage)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stamp := now()
	result, err := tx.ExecContext(ctx, `INSERT INTO chat_messages(session_id,message,usage,created_at) VALUES(?,?,?,?)`, id, string(raw), string(u), stamp)
	if err != nil {
		return 0, err
	}
	title := ""
	if message.Role == providerapi.RoleUser {
		r := []rune(message.Content)
		if len(r) > 80 {
			r = r[:80]
		}
		title = string(r)
	}
	_, err = tx.ExecContext(ctx, `UPDATE chat_sessions SET updated_at=?,title=CASE WHEN title='' THEN ? ELSE title END WHERE session_id=?`, stamp, title, id)
	if err != nil {
		return 0, err
	}
	recordID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return recordID, tx.Commit()
}
func (s *Store) Records(ctx context.Context, id string, after int64) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT message_id,message,usage,created_at FROM chat_messages WHERE session_id=? AND message_id>? ORDER BY message_id`, id, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		var m, u string
		if err := rows.Scan(&r.ID, &m, &u, &r.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(m), &r.Message); err != nil {
			return nil, fmt.Errorf("decode message %d: %w", r.ID, err)
		}
		if err := json.Unmarshal([]byte(u), &r.Usage); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) SetSummary(ctx context.Context, id, summary string, through int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chat_sessions SET summary=?,summary_through=?,summary_replay='' WHERE session_id=?`, summary, through, id)
	return err
}

// Acquire prevents two ymz processes from interleaving a conversation. A crashed
// process releases ownership through expiry; live turns renew every 30 seconds.
func (s *Store) Acquire(ctx context.Context, id string) (string, error) {
	token := NewID()
	until := time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `UPDATE chat_sessions SET lease_owner=?,lease_until=? WHERE session_id=? AND (lease_owner='' OR lease_until<?)`, token, until, id, now())
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", ErrBusy
	}
	return token, nil
}
func (s *Store) Renew(ctx context.Context, id, token string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE chat_sessions SET lease_until=? WHERE session_id=? AND lease_owner=?`, time.Now().UTC().Add(2*time.Minute).Format(time.RFC3339Nano), id, token)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrBusy
	}
	return err
}
func (s *Store) Release(ctx context.Context, id, token string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chat_sessions SET lease_owner='',lease_until='' WHERE session_id=? AND lease_owner=?`, id, token)
	return err
}

func (s *Store) SetNativeSummary(ctx context.Context, id, summary string, through int64, replay *providerapi.Replay) error {
	raw, err := json.Marshal(replay)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE chat_sessions SET summary=?,summary_through=?,summary_replay=? WHERE session_id=?`, summary, through, string(raw), id)
	return err
}
