package agent

import (
	"strings"
	"sync"
)

// InboxItem is one identified user-role message waiting for the next step.
type InboxItem struct {
	ID      string
	Session string
	RunID   string
	Text    string
	// Persisted is true when chatsession already wrote this user row to
	// agent_run_records. The runner then injects it into the provider view only.
	Persisted bool
}

// Inbox is the process-local next-step queue for one Runner (ADR-052).
// R3 persists steer to the session transcript before enqueue; this queue is
// the live driver view. There is no next-turn queue: idle Enter submits a
// new task. Crash recovery rebuilds next-step from durable user rows that
// landed after the last assistant of this run.
type Inbox struct {
	mu   sync.Mutex
	next []InboxItem
}

// NewInbox returns an empty process-local inbox (tests and fakes).
func NewInbox() *Inbox {
	return &Inbox{}
}

// Enqueue appends identified text for session. Empty text is ignored.
func (in *Inbox) Enqueue(item InboxItem) {
	if in == nil {
		return
	}
	item.ID = strings.TrimSpace(item.ID)
	item.Session = strings.TrimSpace(item.Session)
	item.RunID = strings.TrimSpace(item.RunID)
	item.Text = strings.TrimSpace(item.Text)
	if item.Text == "" || item.Session == "" {
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, existing := range in.next {
		if existing.ID != "" && existing.ID == item.ID {
			return
		}
	}
	in.next = append(in.next, item)
}

// Steer queues text for the nearest later step of this run (does not wake).
func (in *Inbox) Steer(sessionID, runID, id, text string) {
	in.Enqueue(InboxItem{ID: id, Session: sessionID, RunID: runID, Text: text})
}

// ClaimStep removes next-step items for this session+run (FIFO).
// Empty runID matches only items that also have an empty RunID.
func (in *Inbox) ClaimStep(sessionID, runID string) []InboxItem {
	if in == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	runID = strings.TrimSpace(runID)
	if sessionID == "" {
		return nil
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	var claimed []InboxItem
	kept := in.next[:0]
	for _, item := range in.next {
		if item.Session != sessionID || item.RunID != runID {
			kept = append(kept, item)
			continue
		}
		claimed = append(claimed, item)
	}
	if len(kept) == 0 {
		in.next = nil
	} else {
		in.next = kept
	}
	return claimed
}

// Pending reports whether session has queued next-step items.
func (in *Inbox) Pending(sessionID string) bool {
	if in == nil {
		return false
	}
	sessionID = strings.TrimSpace(sessionID)
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, item := range in.next {
		if item.Session == sessionID {
			return true
		}
	}
	return false
}

// Clear drops queued items for session. Empty sessionID clears the whole inbox.
func (in *Inbox) Clear(sessionID string) {
	if in == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	in.mu.Lock()
	defer in.mu.Unlock()
	if sessionID == "" {
		in.next = nil
		return
	}
	kept := in.next[:0]
	for _, item := range in.next {
		if item.Session != sessionID {
			kept = append(kept, item)
		}
	}
	if len(kept) == 0 {
		in.next = nil
	} else {
		in.next = kept
	}
}
