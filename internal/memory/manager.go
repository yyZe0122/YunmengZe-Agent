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
)

// Manager curates user preferences and freezes injection for each run.
type Manager struct {
	store          *Store
	maxInjectRunes int
	defaultTTL     time.Duration
	now            func() time.Time
	mu             sync.Mutex
	curatorMu      sync.Mutex
	closed         bool
	// Run snapshots keyed by session; the engine invalidates at the next run.
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

// Initialize opens lifecycle and archives expired legacy entries (no hard delete).
func (m *Manager) Initialize(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = false
	m.maintainLocked(ctx, "initialize")
	return nil
}

// Maintain archives expired legacy entries. Preferences expire without tombstones.
func (m *Manager) Maintain(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.maintainLocked(ctx, "maintain")
}

func (m *Manager) maintainLocked(ctx context.Context, operation string) {
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
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.snapshots = make(map[string]string)
	return nil
}

// InvalidateSnapshot drops the frozen inject block for a session (or all if empty).
func (m *Manager) InvalidateSnapshot(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		m.snapshots = make(map[string]string)
		return
	}
	delete(m.snapshots, sessionID)
}

// FrozenSystemBlock returns preferences fixed for this run until invalidated.
func (m *Manager) FrozenSystemBlock(ctx context.Context, sessionID string) string {
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

func (m *Manager) buildInjectBlock(ctx context.Context, sessionID string) string {
	nowRFC := m.now().UTC().Format(time.RFC3339Nano)
	entries, err := m.store.Preferences(ctx, 32, nowRFC)
	if err != nil || len(entries) == 0 {
		return ""
	}
	return formatBlock(entries, m.maxInjectRunes)
}

// List returns current facts for this session and optionally the user.
func (m *Manager) List(ctx context.Context, sessionID string, includeGlobal bool, limit int) ([]Entry, error) {
	nowRFC := m.now().UTC().Format(time.RFC3339Nano)
	return m.store.ListRecent(ctx, sessionID, includeGlobal, true, limit, nowRFC)
}

// Search lists matching entries for UI/tools.
func (m *Manager) Search(ctx context.Context, sessionID, query string, limit int) ([]Entry, error) {
	nowRFC := m.now().UTC().Format(time.RFC3339Nano)
	return m.store.Search(ctx, sessionID, query, true, limit, nowRFC)
}

func formatBlock(entries []Entry, maxRunes int) string {
	var b strings.Builder
	b.WriteString("User preferences (may be corrected by the current user; never evidence of project or environment state):\n")
	for _, e := range entries {

		prefix := ""
		if e.SessionID == "" || e.Kind == KindCurated {
			prefix = "[user] "
		}
		line := "- " + prefix + truncateRunes(e.Content, 240)
		if utf8.RuneCountInString(b.String())+utf8.RuneCountInString(line)+1 > maxRunes {
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
