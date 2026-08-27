// Package editrev records agent file-edit checkpoints for human rewind (ADR-051 / QG).
package editrev

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/artifacts"
	"github.com/yyZe0122/yunmengze-agent/internal/runmeta"
)

const (
	KindModify = "modify"
	KindCreate = "create"
	KindMkdir  = "mkdir"
	KindDelete = "delete"
)

// Revision is one agent write checkpoint.
type Revision struct {
	ID         string
	SessionID  string
	Path       string
	SHABefore  string
	SHAAfter   string
	ArtifactID string
	RunID      string
	Kind       string
	CreatedAt  string
}

// Store snapshots bytes to the existing artifact store and rows in edit_revisions.
type Store struct {
	db        *sql.DB
	artifacts *artifacts.Store
	now       func() time.Time
}

func NewStore(db *sql.DB, arts *artifacts.Store) (*Store, error) {
	if db == nil || arts == nil {
		return nil, errors.New("editrev requires database and artifact store")
	}
	return &Store{db: db, artifacts: arts, now: func() time.Time { return time.Now().UTC() }}, nil
}

func normalizeKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case KindCreate, KindMkdir, KindDelete:
		return kind
	default:
		return KindModify
	}
}

func inferKind(rev Revision) string {
	kind := normalizeKind(rev.Kind)
	if kind != KindModify {
		return kind
	}
	if strings.TrimSpace(rev.SHABefore) != "" || strings.TrimSpace(rev.ArtifactID) != "" {
		return KindModify
	}
	if strings.TrimSpace(rev.SHAAfter) != "" {
		return KindCreate
	}
	return KindMkdir
}

// SnapshotBeforeWrite stores old bytes (fail-closed). Skips symlinks.
func (s *Store) SnapshotBeforeWrite(ctx context.Context, path string, before []byte, shaAfter, kind string) error {
	if s == nil {
		return errors.New("editrev store is nil")
	}
	info, err := os.Lstat(path)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to snapshot symlink")
	}
	sessionID, runID := "", ""
	if meta, ok := runmeta.From(ctx); ok {
		sessionID = strings.TrimSpace(meta.SessionID)
		runID = strings.TrimSpace(meta.RunID)
	}
	if sessionID == "" {
		return errors.New("session id is required for edit checkpoint")
	}
	kind = normalizeKind(kind)
	shaBefore := ""
	artifactID := ""
	if len(before) > 0 {
		sum := sha256.Sum256(before)
		shaBefore = hex.EncodeToString(sum[:])
		ref, putErr := s.artifacts.Put(ctx, "application/octet-stream", before, map[string]any{
			"kind": "edit_revision", "path": path, "session_id": sessionID,
		})
		if putErr != nil {
			return fmt.Errorf("snapshot artifact: %w", putErr)
		}
		artifactID = ref.ID
	}
	id := "erev-" + sha256hex(sessionID, path, shaBefore, shaAfter, kind, s.now().UTC().Format(time.RFC3339Nano))[:32]
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO edit_revisions (revision_id, session_id, path, sha_before, sha_after, artifact_id, run_id, kind, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, sessionID, path, shaBefore, shaAfter, artifactID, runID, kind, s.now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert edit revision: %w", err)
	}
	return nil
}

func (s *Store) Latest(ctx context.Context, sessionID string) (Revision, error) {
	return s.get(ctx, sessionID, "")
}

func (s *Store) Get(ctx context.Context, sessionID, revisionID string) (Revision, error) {
	return s.get(ctx, sessionID, revisionID)
}

func (s *Store) get(ctx context.Context, sessionID, revisionID string) (Revision, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Revision{}, errors.New("session id is required")
	}
	var row Revision
	var err error
	if id := strings.TrimSpace(revisionID); id != "" {
		err = s.db.QueryRowContext(ctx, `
			SELECT revision_id, session_id, path, sha_before, sha_after, artifact_id, run_id, kind, created_at
			FROM edit_revisions WHERE session_id = ? AND revision_id = ?`, sessionID, id).
			Scan(&row.ID, &row.SessionID, &row.Path, &row.SHABefore, &row.SHAAfter, &row.ArtifactID, &row.RunID, &row.Kind, &row.CreatedAt)
	} else {
		err = s.db.QueryRowContext(ctx, `
			SELECT revision_id, session_id, path, sha_before, sha_after, artifact_id, run_id, kind, created_at
			FROM edit_revisions WHERE session_id = ? ORDER BY created_at DESC LIMIT 1`, sessionID).
			Scan(&row.ID, &row.SessionID, &row.Path, &row.SHABefore, &row.SHAAfter, &row.ArtifactID, &row.RunID, &row.Kind, &row.CreatedAt)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, fmt.Errorf("no edit revision")
	}
	if err != nil {
		return Revision{}, err
	}
	row.Kind = normalizeKind(row.Kind)
	return row, nil
}

func (s *Store) ListByRunIDs(ctx context.Context, sessionID string, runIDs []string) ([]Revision, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("session id is required")
	}
	ids := make([]string, 0, len(runIDs))
	for _, id := range runIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, 1+len(ids))
	args = append(args, sessionID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT revision_id, session_id, path, sha_before, sha_after, artifact_id, run_id, kind, created_at
		FROM edit_revisions
		WHERE session_id = ? AND run_id IN (`+placeholders+`)
		ORDER BY created_at DESC, revision_id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Revision, 0)
	for rows.Next() {
		var row Revision
		if err := rows.Scan(&row.ID, &row.SessionID, &row.Path, &row.SHABefore, &row.SHAAfter, &row.ArtifactID, &row.RunID, &row.Kind, &row.CreatedAt); err != nil {
			return nil, err
		}
		row.Kind = normalizeKind(row.Kind)
		out = append(out, row)
	}
	return out, rows.Err()
}

// Rewind restores the path according to kind. Fail-closed if disk drifted.
func (s *Store) Rewind(ctx context.Context, sessionID, revisionID string) (Revision, error) {
	rev, err := s.get(ctx, sessionID, revisionID)
	if err != nil {
		return Revision{}, err
	}
	switch inferKind(rev) {
	case KindCreate:
		return s.rewindCreate(rev)
	case KindMkdir:
		return s.rewindMkdir(rev)
	case KindDelete:
		return s.rewindDelete(ctx, rev)
	default:
		return s.rewindModify(ctx, rev)
	}
}

func (s *Store) rewindCreate(rev Revision) (Revision, error) {
	info, err := os.Lstat(rev.Path)
	if errors.Is(err, os.ErrNotExist) {
		return rev, nil
	}
	if err != nil {
		return Revision{}, err
	}
	if info.IsDir() {
		return Revision{}, fmt.Errorf("create rewind expected file: %s", rev.Path)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Revision{}, fmt.Errorf("refusing to rewind symlink")
	}
	current, err := os.ReadFile(rev.Path)
	if err != nil {
		return Revision{}, err
	}
	if len(current) > 0 && rev.SHAAfter != "" {
		sum := sha256.Sum256(current)
		if hex.EncodeToString(sum[:]) != rev.SHAAfter {
			return Revision{}, fmt.Errorf("file changed since revision; refuse rewind")
		}
	}
	if err := os.Remove(rev.Path); err != nil {
		return Revision{}, err
	}
	return rev, nil
}

func (s *Store) rewindMkdir(rev Revision) (Revision, error) {
	info, err := os.Lstat(rev.Path)
	if errors.Is(err, os.ErrNotExist) {
		return rev, nil
	}
	if err != nil {
		return Revision{}, err
	}
	if !info.IsDir() {
		return Revision{}, fmt.Errorf("mkdir rewind expected directory: %s", rev.Path)
	}
	if err := os.Remove(rev.Path); err != nil {
		return Revision{}, err
	}
	return rev, nil
}

func (s *Store) rewindDelete(ctx context.Context, rev Revision) (Revision, error) {
	_, err := os.Lstat(rev.Path)
	if err == nil {
		current, readErr := os.ReadFile(rev.Path)
		if readErr != nil {
			return Revision{}, readErr
		}
		if rev.SHABefore != "" {
			sum := sha256.Sum256(current)
			if hex.EncodeToString(sum[:]) != rev.SHABefore {
				return Revision{}, fmt.Errorf("file changed since revision; refuse rewind")
			}
		}
		return rev, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Revision{}, err
	}
	old, err := s.artifactBytes(ctx, rev)
	if err != nil {
		return Revision{}, err
	}
	if err := os.MkdirAll(filepath.Dir(rev.Path), 0o750); err != nil {
		return Revision{}, err
	}
	if err := os.WriteFile(rev.Path, old, 0o640); err != nil {
		return Revision{}, err
	}
	return rev, nil
}

func (s *Store) rewindModify(ctx context.Context, rev Revision) (Revision, error) {
	current, err := os.ReadFile(rev.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Revision{}, err
	}
	if len(current) > 0 && rev.SHAAfter != "" {
		sum := sha256.Sum256(current)
		if hex.EncodeToString(sum[:]) != rev.SHAAfter {
			return Revision{}, fmt.Errorf("file changed since revision; refuse rewind")
		}
	}
	old, err := s.artifactBytes(ctx, rev)
	if err != nil {
		return Revision{}, err
	}
	if err := os.MkdirAll(filepath.Dir(rev.Path), 0o750); err != nil {
		return Revision{}, err
	}
	if err := os.WriteFile(rev.Path, old, 0o640); err != nil {
		return Revision{}, err
	}
	return rev, nil
}

func (s *Store) artifactBytes(ctx context.Context, rev Revision) ([]byte, error) {
	if rev.ArtifactID == "" {
		return nil, nil
	}
	return s.artifacts.Get(ctx, rev.ArtifactID)
}

func sha256hex(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
