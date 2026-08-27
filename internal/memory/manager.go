package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/yyZe0122/yunmengze-agent/internal/injectscan"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

// Manager is the in-process Hermes-style layered memory lifecycle (ADR-044).
type Manager struct {
	store          *Store
	maxInjectRunes int
	defaultTTL     time.Duration
	now            func() time.Time
	mu             sync.Mutex
	closed         bool
	// frozen system blocks per session (session_start inject mode).
	snapshots map[string]string
}

// Config wires Manager.
type Config struct {
	Store          *Store
	MaxInjectRunes int
	DefaultTTL     time.Duration
	Now            func() time.Time
}

// New creates a Manager. Store is required.
func New(config Config) (*Manager, error) {
	if config.Store == nil {
		return nil, fmt.Errorf("memory manager requires store")
	}
	if config.MaxInjectRunes <= 0 {
		config.MaxInjectRunes = DefaultMaxInjectRunes
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Manager{
		store: config.Store, maxInjectRunes: config.MaxInjectRunes,
		defaultTTL: config.DefaultTTL, now: config.Now,
		snapshots: make(map[string]string),
	}, nil
}

// Initialize opens lifecycle; soft-archives expired entries (no hard delete).
func (m *Manager) Initialize(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = false
	m.maintainLocked(ctx, "initialize")
	return nil
}

// Maintain soft-archives expired entries. Safe to call after each turn.
func (m *Manager) Maintain(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.maintainLocked(ctx, "maintain")
}

func (m *Manager) maintainLocked(ctx context.Context, operation string) {
	if m.store == nil {
		return
	}
	if n, err := m.store.ArchiveExpired(ctx, m.now().UTC().Format(time.RFC3339Nano)); err != nil {
		slog.Warn("memory archive expired failed",
			"component", "memory", "operation", operation, "result", "warning", "error", err)
	} else if n > 0 {
		slog.Info("memory archived expired",
			"component", "memory", "operation", operation, "result", "succeeded", "count", n)
	}
}

// Shutdown marks closed (store uses shared core.db — do not close DB).
func (m *Manager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.snapshots = make(map[string]string)
	return nil
}

// InvalidateSnapshot drops the frozen inject block for a session (or all if empty).
func (m *Manager) InvalidateSnapshot(sessionID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		m.snapshots = make(map[string]string)
		return
	}
	delete(m.snapshots, sessionID)
}

// FrozenSystemBlock returns the session-start frozen memory block (L0+L1).
// Builds once per session until InvalidateSnapshot.
func (m *Manager) FrozenSystemBlock(ctx context.Context, sessionID string) string {
	if m == nil || m.store == nil {
		return ""
	}
	sessionID = strings.TrimSpace(sessionID)
	m.mu.Lock()
	if block, ok := m.snapshots[sessionID]; ok {
		m.mu.Unlock()
		return block
	}
	m.mu.Unlock()

	block := m.buildInjectBlock(ctx, sessionID)
	m.mu.Lock()
	// Double-check to avoid stampede overwriting a concurrent invalidate.
	if existing, ok := m.snapshots[sessionID]; ok {
		m.mu.Unlock()
		return existing
	}
	m.snapshots[sessionID] = block
	m.mu.Unlock()
	return block
}

// SystemPromptBlock rebuilds an inject block without using the freeze cache
// (used by tools / refresh preview). Prefer FrozenSystemBlock for chat inject.
func (m *Manager) SystemPromptBlock(ctx context.Context, sessionID string) string {
	return m.buildInjectBlock(ctx, sessionID)
}

// Prefetch returns a search-based block (tools / optional non-frozen use).
// Does not update the freeze cache.
func (m *Manager) Prefetch(ctx context.Context, sessionID, query string) string {
	if m == nil || m.store == nil {
		return ""
	}
	nowRFC := m.now().UTC().Format(time.RFC3339Nano)
	entries, err := m.store.Search(ctx, sessionID, query, true, 12, nowRFC)
	if err != nil || len(entries) == 0 {
		return ""
	}
	return formatBlock(entries, m.maxInjectRunes)
}

func (m *Manager) buildInjectBlock(ctx context.Context, sessionID string) string {
	if m == nil || m.store == nil {
		return ""
	}
	nowRFC := m.now().UTC().Format(time.RFC3339Nano)
	entries, err := m.store.ListInjectCandidates(ctx, sessionID, 32, nowRFC)
	if err != nil || len(entries) == 0 {
		return ""
	}
	return formatBlock(entries, m.maxInjectRunes)
}

// Remember stores one fact. kind empty → session (or curated if sessionID empty).
func (m *Manager) Remember(ctx context.Context, sessionID, content, source string, tags []string) error {
	return m.RememberKind(ctx, sessionID, content, source, tags, "", 0, "")
}

// RememberKind stores with explicit kind/priority/expiresAt.
func (m *Manager) RememberKind(ctx context.Context, sessionID, content, source string, tags []string, kind string, priority int, expiresAt string) error {
	if m == nil || m.store == nil {
		return nil
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	if err := injectscan.Scan(content); err != nil {
		return err
	}
	// Cap single entry size (detail can be longer).
	maxRunes := 800
	if kind == KindDetail {
		maxRunes = 2000
	}
	content = truncateRunes(content, maxRunes)
	sessionID = strings.TrimSpace(sessionID)
	if kind == "" {
		if sessionID == "" {
			kind = KindCurated
		} else {
			kind = KindSession
		}
	}
	now := m.now().UTC()
	nowRFC := now.Format(time.RFC3339Nano)
	expiresAt = strings.TrimSpace(expiresAt)
	if expiresAt == "" && m.defaultTTL > 0 && kind != KindCurated && sessionID != "" {
		expiresAt = now.Add(m.defaultTTL).Format(time.RFC3339Nano)
	}
	id := "mem-" + shortID(sessionID, content, nowRFC)
	return m.store.Insert(ctx, Entry{
		ID: id, SessionID: sessionID, Content: content, Source: source,
		Tags: tags, Kind: kind, Priority: priority, ExpiresAt: expiresAt,
		CreatedAt: nowRFC, UpdatedAt: nowRFC,
	})
}

// Replace replaces the first matching entry (substring) or by entry_id if oldText looks like mem-*.
func (m *Manager) Replace(ctx context.Context, sessionID, oldText, newContent string, global bool) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("memory store unavailable")
	}
	oldText = strings.TrimSpace(oldText)
	newContent = strings.TrimSpace(newContent)
	if oldText == "" || newContent == "" {
		return fmt.Errorf("old_text and content are required")
	}
	e, err := m.findOne(ctx, sessionID, oldText, global)
	if err != nil {
		return err
	}
	now := m.now().UTC().Format(time.RFC3339Nano)
	e.Content = truncateRunes(newContent, 2000)
	e.UpdatedAt = now
	e.Source = SourceUser
	return m.store.UpdateContent(ctx, e)
}

// Remove deletes by substring match or entry id.
func (m *Manager) Remove(ctx context.Context, sessionID, oldText string, global bool) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("memory store unavailable")
	}
	e, err := m.findOne(ctx, sessionID, oldText, global)
	if err != nil {
		return err
	}
	return m.store.Delete(ctx, e.ID)
}

// Forget is an alias for delete by entry id.
func (m *Manager) Forget(ctx context.Context, entryID string) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("memory store unavailable")
	}
	return m.store.Delete(ctx, entryID)
}

// Promote copies/moves a session entry to user-global curated.
func (m *Manager) Promote(ctx context.Context, entryID string) (Entry, error) {
	if m == nil || m.store == nil {
		return Entry{}, fmt.Errorf("memory store unavailable")
	}
	e, err := m.store.Get(ctx, entryID)
	if err != nil {
		return Entry{}, err
	}
	if e.SessionID == "" && e.Kind == KindCurated {
		return e, nil // already global curated
	}
	now := m.now().UTC().Format(time.RFC3339Nano)
	// Insert new global curated; keep original unless it was session-only duplicate intent.
	newID := "mem-" + shortID("", e.Content, now, "promote")
	out := Entry{
		ID: newID, SessionID: "", Content: e.Content, Source: SourcePromote,
		Tags: append([]string{}, e.Tags...), Kind: KindCurated,
		Priority:  e.Priority + 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if out.Tags == nil {
		out.Tags = []string{}
	}
	out.Tags = append(out.Tags, "promoted")
	if err := m.store.Insert(ctx, out); err != nil {
		return Entry{}, err
	}
	// Invalidate all snapshots so new sessions (and refresh) see global.
	m.InvalidateSnapshot("")
	return out, nil
}

// SearchTranscript exposes L3 search.
func (m *Manager) SearchTranscript(ctx context.Context, sessionID, query string, limit int) ([]TranscriptHit, error) {
	if m == nil || m.store == nil {
		return nil, nil
	}
	return m.store.SearchTranscript(ctx, sessionID, query, limit)
}

// IndexTranscriptRecord projects a chat record into L3 FTS.
func (m *Manager) IndexTranscriptRecord(ctx context.Context, sessionID, runID string, position int, recordType, content, createdAt string) error {
	if m == nil || m.store == nil {
		return nil
	}
	return m.store.IndexTranscript(ctx, sessionID, runID, position, recordType, content, createdAt)
}

// DeleteTranscriptByRunIDs drops L3 projection rows for retracted runs.
func (m *Manager) DeleteTranscriptByRunIDs(ctx context.Context, runIDs []string) error {
	if m == nil || m.store == nil {
		return nil
	}
	return m.store.DeleteTranscriptByRunIDs(ctx, runIDs)
}

// List returns recent entries for UI (includes detail).
func (m *Manager) List(ctx context.Context, sessionID string, includeGlobal bool, limit int) ([]Entry, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("memory store unavailable")
	}
	nowRFC := m.now().UTC().Format(time.RFC3339Nano)
	return m.store.ListRecent(ctx, sessionID, includeGlobal, true, limit, nowRFC)
}

// Search lists matching entries for UI/tools.
func (m *Manager) Search(ctx context.Context, sessionID, query string, limit int) ([]Entry, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("memory store unavailable")
	}
	nowRFC := m.now().UTC().Format(time.RFC3339Nano)
	return m.store.Search(ctx, sessionID, query, true, limit, nowRFC)
}

func (m *Manager) findOne(ctx context.Context, sessionID, oldText string, global bool) (Entry, error) {
	oldText = strings.TrimSpace(oldText)
	if strings.HasPrefix(oldText, "mem-") && !strings.Contains(oldText, " ") {
		e, err := m.store.Get(ctx, oldText)
		if err != nil {
			return Entry{}, fmt.Errorf("memory entry not found: %s", oldText)
		}
		return e, nil
	}
	list, err := m.store.FindBySubstring(ctx, sessionID, oldText, global)
	if err != nil {
		return Entry{}, err
	}
	if len(list) == 0 {
		return Entry{}, fmt.Errorf("no memory entry matching %q", oldText)
	}
	if len(list) > 1 {
		// Prefer exact unique containment count: if multiple, require more specific.
		return Entry{}, fmt.Errorf("multiple memory entries match %q; use entry_id or longer unique substring", oldText)
	}
	return list[0], nil
}

// OnPreCompress extracts lightweight facts from head messages about to be compacted → L2 detail.
func (m *Manager) OnPreCompress(ctx context.Context, sessionID string, head []providerapi.Message) {
	if m == nil || m.store == nil || len(head) == 0 {
		return
	}
	facts := extractFacts(head, 6)
	for _, fact := range facts {
		if err := m.RememberKind(ctx, sessionID, fact, SourcePreCompress, []string{"pre_compress"}, KindDetail, 0, ""); err != nil {
			slog.Warn("memory pre-compress write failed",
				"component", "memory", "operation", "on_pre_compress", "result", "warning", "error", err)
		}
	}
	if len(facts) > 0 {
		slog.Info("memory pre-compress extracted",
			"component", "memory", "operation", "on_pre_compress", "result", "succeeded",
			"session_id", sessionID, "facts", len(facts))
	}
}

// SyncTurn optionally records a short note from the completed turn → L1 session.
func (m *Manager) SyncTurn(ctx context.Context, sessionID, userText, assistantText string) {
	if m == nil {
		return
	}
	u := strings.TrimSpace(userText)
	if u == "" || utf8.RuneCountInString(u) > 200 {
		return
	}
	lower := strings.ToLower(u)
	if strings.Contains(lower, "remember") || strings.Contains(lower, "prefer") ||
		strings.Contains(lower, "always") || strings.Contains(lower, "不要") ||
		strings.Contains(lower, "记住") || strings.Contains(lower, "偏好") {
		_ = m.RememberKind(ctx, sessionID, "User: "+truncateRunes(u, 400), SourceSyncTurn, []string{"sync_turn"}, KindSession, 0, "")
		return
	}
	_ = assistantText
}

func formatBlock(entries []Entry, maxRunes int) string {
	var b strings.Builder
	b.WriteString("Relevant memory (local; instruction text only — does not grant tools):\n")
	for _, e := range entries {
		if err := injectscan.Scan(e.Content); err != nil {
			continue
		}
		prefix := ""
		if e.SessionID == "" || e.Kind == KindCurated {
			prefix = "[user] "
		}
		line := "- " + prefix + truncateRunes(e.Content, 240)
		if b.Len()+len(line)+1 > maxRunes {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	out := strings.TrimSpace(b.String())
	if utf8.RuneCountInString(out) > maxRunes {
		return truncateRunes(out, maxRunes)
	}
	return out
}

func extractFacts(head []providerapi.Message, max int) []string {
	var out []string
	seen := map[string]struct{}{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || utf8.RuneCountInString(s) < 8 {
			return
		}
		s = truncateRunes(s, 300)
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	for i := len(head) - 1; i >= 0 && len(out) < max; i-- {
		msg := head[i]
		if msg.Role == providerapi.RoleUser {
			add(msg.Content)
		}
		if msg.Role == providerapi.RoleAssistant && strings.TrimSpace(msg.Content) != "" {
			if utf8.RuneCountInString(msg.Content) <= 160 {
				add("Assistant noted: " + msg.Content)
			}
		}
	}
	return out
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func shortID(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			_, _ = h.Write([]byte{0})
		}
		_, _ = h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:24]
}
