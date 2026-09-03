package toolpermission

import (
	"context"
	"sync"
	"time"
)

// Decision is delivered to a waiting Broker after user decide.
type Decision struct {
	PermissionID string
	Decision     string // allow_once | allow_similar | allow_permanent | deny
	GrantID      string
	State        string
}

// Waiter coordinates in-process Broker waits with Gateway decide.
type Waiter struct {
	mu   sync.Mutex
	wait map[string]chan Decision
}

func NewWaiter() *Waiter {
	return &Waiter{wait: make(map[string]chan Decision)}
}

// Register prepares a wait channel for permissionID (buffer 1).
func (w *Waiter) Register(permissionID string) {
	if w == nil || permissionID == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.wait[permissionID]; ok {
		return
	}
	w.wait[permissionID] = make(chan Decision, 1)
}

// Wait blocks until Decide notifies or ctx ends.
func (w *Waiter) Wait(ctx context.Context, permissionID string) (Decision, error) {
	if w == nil {
		return Decision{}, context.Canceled
	}
	w.mu.Lock()
	ch, ok := w.wait[permissionID]
	if !ok {
		ch = make(chan Decision, 1)
		w.wait[permissionID] = ch
	}
	w.mu.Unlock()

	select {
	case <-ctx.Done():
		w.cleanup(permissionID)
		return Decision{}, ctx.Err()
	case d := <-ch:
		w.cleanup(permissionID)
		return d, nil
	}
}

// Notify unblocks Wait with the decision (non-blocking if no waiter).
func (w *Waiter) Notify(d Decision) {
	if w == nil || d.PermissionID == "" {
		return
	}
	w.mu.Lock()
	ch, ok := w.wait[d.PermissionID]
	w.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- d:
	default:
	}
}

func (w *Waiter) cleanup(permissionID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.wait, permissionID)
}

// WaitTimeout is the default max wait for interactive permission.
const WaitTimeout = 30 * time.Minute
