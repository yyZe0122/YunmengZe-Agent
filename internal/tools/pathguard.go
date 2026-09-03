package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yyZe0122/yunmengze-agent/internal/platform/pathsecurity"
	"github.com/yyZe0122/yunmengze-agent/internal/runmeta"
)

// ErrPathEscapes is returned when a path resolves outside configured roots.
var ErrPathEscapes = errors.New("path escapes configured filesystem roots")

// PathGuard contains tool paths under configured roots (ADR-012 / ADR-046).
// Roots may grow via AddRoot when sessions bind a client workspace.
// Session extra-roots (allow_similar) and once-call roots stay off the global ceiling.
type PathGuard struct {
	mu           sync.RWMutex
	roots        []string
	allowAll     bool
	sessionRoots map[string][]string
	onceRoots    map[string][]string
}

func NewPathGuard(roots []string) (*PathGuard, error) {
	return newPathGuard(roots, false)
}

// NewPathGuardAllowAll builds a guard that only requires absolute/resolved paths
// (no root containment). For local single-user chat.workspace.allow_all only.
func NewPathGuardAllowAll() *PathGuard {
	return &PathGuard{allowAll: true, sessionRoots: map[string][]string{}, onceRoots: map[string][]string{}}
}

func newPathGuard(roots []string, allowAll bool) (*PathGuard, error) {
	if allowAll {
		return &PathGuard{allowAll: true}, nil
	}
	if len(roots) == 0 {
		return nil, errors.New("at least one tool filesystem root is required")
	}
	g := &PathGuard{sessionRoots: map[string][]string{}, onceRoots: map[string][]string{}}
	for _, root := range roots {
		if err := g.addRootLocked(root); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// AddRoot appends an absolute workspace root after symlink resolve.
// No-op when allow_all. Duplicate roots are ignored.
func (g *PathGuard) AddRoot(root string) error {
	if g == nil {
		return errors.New("path guard is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.allowAll {
		return nil
	}
	return g.addToLocked(&g.roots, root)
}

// AddSessionRoot adds an extra-root for one session (allow_similar). Other sessions stay contained.
func (g *PathGuard) AddSessionRoot(sessionID, root string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session id is required")
	}
	if g == nil {
		return errors.New("path guard is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.allowAll {
		return nil
	}
	if g.sessionRoots == nil {
		g.sessionRoots = map[string][]string{}
	}
	list := g.sessionRoots[sessionID]
	if err := g.addToLocked(&list, root); err != nil {
		return err
	}
	g.sessionRoots[sessionID] = list
	return nil
}

// AddOnceRoot allows one tool call (allow_once extra-root retry) without raising the process ceiling.
func (g *PathGuard) AddOnceRoot(callID, root string) error {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return errors.New("tool call id is required")
	}
	if g == nil {
		return errors.New("path guard is nil")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.allowAll {
		return nil
	}
	if g.onceRoots == nil {
		g.onceRoots = map[string][]string{}
	}
	list := g.onceRoots[callID]
	if err := g.addToLocked(&list, root); err != nil {
		return err
	}
	g.onceRoots[callID] = list
	return nil
}

// Roots returns a copy of configured roots (empty when allow_all).
func (g *PathGuard) Roots() []string {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.allowAll {
		return nil
	}
	return append([]string(nil), g.roots...)
}

// AllowAll reports unrestricted root mode.
func (g *PathGuard) AllowAll() bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.allowAll
}

func (g *PathGuard) addRootLocked(root string) error {
	return g.addToLocked(&g.roots, root)
}

func (g *PathGuard) addToLocked(dst *[]string, root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return errors.New("filesystem root cannot be empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve filesystem root: %w", err)
	}
	real, err := pathsecurity.ResolveExisting(absolute)
	if err != nil {
		return fmt.Errorf("resolve filesystem root symlinks: %w", err)
	}
	for _, existing := range *dst {
		if existing == real {
			return nil
		}
	}
	*dst = append(*dst, real)
	return nil
}

func (g *PathGuard) extraRootsLocked(sessionID, callID string) []string {
	var extra []string
	if sessionID != "" && g.sessionRoots != nil {
		extra = append(extra, g.sessionRoots[sessionID]...)
	}
	if callID != "" && g.onceRoots != nil {
		extra = append(extra, g.onceRoots[callID]...)
	}
	return extra
}

// Resolve rejects lexical traversal and symlink traversal outside configured
// roots. Callers must still open or rename promptly to minimize TOCTOU windows.
func (g *PathGuard) Resolve(value string) (string, error) {
	return g.ResolveContext(context.Background(), value)
}

// ResolveContext is Resolve plus session extra-roots and once-call roots from runmeta.
func (g *PathGuard) ResolveContext(ctx context.Context, value string) (string, error) {
	if g == nil {
		return "", errors.New("path guard is nil")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("path is required")
	}
	sessionID, callID := extraRootKeys(ctx)
	g.mu.RLock()
	allowAll := g.allowAll
	roots := append([]string(nil), g.roots...)
	roots = append(roots, g.extraRootsLocked(sessionID, callID)...)
	g.mu.RUnlock()

	if !filepath.IsAbs(value) {
		return g.resolveRelative(value, roots, allowAll)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	real, err := pathsecurity.ResolveExisting(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve path symlinks: %w", err)
	}
	if allowAll {
		return real, nil
	}
	for _, root := range roots {
		if pathsecurity.Contains(root, real) {
			return real, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrPathEscapes, value)
}

func extraRootKeys(ctx context.Context) (sessionID, callID string) {
	if meta, ok := runmeta.From(ctx); ok {
		sessionID = strings.TrimSpace(meta.SessionID)
		callID = strings.TrimSpace(meta.CallID)
	}
	if callID == "" {
		callID = strings.TrimSpace(toolCallIDFromContext(ctx))
	}
	return sessionID, callID
}

// ResolveAllowOutside is Resolve plus interactive extra-root: an absolute path
// that exists (or has an existing ancestor) may be returned even when it is
// outside configured roots. Relative paths still fail closed. Forbidden roots
// (filesystem volume root) are rejected.
func (g *PathGuard) ResolveAllowOutside(ctx context.Context, value string) (string, error) {
	resolved, err := g.ResolveContext(ctx, value)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, ErrPathEscapes) {
		return "", err
	}
	value = strings.TrimSpace(value)
	if !filepath.IsAbs(value) {
		return "", err
	}
	absolute, absErr := filepath.Abs(value)
	if absErr != nil {
		return "", err
	}
	real, resErr := pathsecurity.ResolveExisting(absolute)
	if resErr != nil {
		return "", err
	}
	if err := pathsecurity.ValidExtraRoot(real); err != nil {
		return "", err
	}
	return real, nil
}

// ResolveForAuth is Resolve plus ExtraRoot=true when the path is outside roots
// but still a valid extra-root candidate for interactive /perm.
func (g *PathGuard) ResolveForAuth(ctx context.Context, value string) (string, bool, error) {
	resolved, err := g.ResolveContext(ctx, value)
	if err == nil {
		return resolved, false, nil
	}
	outside, outsideErr := g.ResolveAllowOutside(ctx, value)
	if outsideErr != nil {
		return "", false, err
	}
	return outside, true, nil
}

// ValidExtraRoot rejects empty, relative, and filesystem-volume roots.
func ValidExtraRoot(root string) error {
	return pathsecurity.ValidExtraRoot(root)
}

func (g *PathGuard) resolveRelative(value string, roots []string, allowAll bool) (string, error) {
	if allowAll {
		absolute, err := filepath.Abs(value)
		if err != nil {
			return "", fmt.Errorf("resolve relative path: %w", err)
		}
		real, err := pathsecurity.ResolveExisting(absolute)
		if err != nil {
			return "", fmt.Errorf("resolve path symlinks: %w", err)
		}
		return real, nil
	}
	var lastErr error
	for _, root := range roots {
		absolute, err := filepath.Abs(filepath.Join(root, value))
		if err != nil {
			lastErr = err
			continue
		}
		real, err := pathsecurity.ResolveExisting(absolute)
		if err != nil {
			lastErr = err
			continue
		}
		for _, r := range roots {
			if pathsecurity.Contains(r, real) {
				return real, nil
			}
		}
	}
	if lastErr != nil {
		return "", fmt.Errorf("resolve relative path: %w", lastErr)
	}
	return "", fmt.Errorf("%w: %s", ErrPathEscapes, value)
}
