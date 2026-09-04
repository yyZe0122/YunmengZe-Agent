// Package modelstream is an in-process fan-out for provider streaming events
// destined for local Gateway clients (TUI typewriter). It is not durable;
// recovery still uses agent_run_records (ADR-031).
package modelstream

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

// Default coalesce window for text/thinking deltas (Crush-style ~33ms).
const DefaultDebounce = 33 * time.Millisecond

// Envelope wraps a StreamEvent with routing ids for multi-subscriber UIs.
type Envelope struct {
	Seq         uint64                  `json:"seq"`
	SessionID   string                  `json:"session_id,omitempty"`
	TaskID      string                  `json:"task_id,omitempty"`
	RunID       string                  `json:"run_id,omitempty"`
	ParentRunID string                  `json:"parent_run_id,omitempty"`
	Event       providerapi.StreamEvent `json:"event"`
}

// Hub is a process-local pub/sub for model stream envelopes.
// Consecutive StreamDelta / StreamThinking for the same run are coalesced
// within Debounce to reduce gateway/TUI pressure.
type Hub struct {
	mu       sync.Mutex
	seq      atomic.Uint64
	subs     map[uint64]*subscription
	nextSub  uint64
	Debounce time.Duration

	// pending coalesced deltas keyed by runID (or sessionID when run empty).
	pending map[string]*pendingBatch
}

type pendingBatch struct {
	sessionID   string
	taskID      string
	runID       string
	parentRunID string
	content     string
	thinking    string
	timer       *time.Timer
}

type subscription struct {
	id        uint64
	sessionID string
	runID     string
	ch        chan Envelope
}

func NewHub() *Hub {
	return &Hub{
		subs:     make(map[uint64]*subscription),
		pending:  make(map[string]*pendingBatch),
		Debounce: DefaultDebounce,
	}
}

func (h *Hub) batchKey(sessionID, runID string) string {
	if runID != "" {
		return "r:" + runID
	}
	return "s:" + sessionID
}

// Publish sends an envelope to matching subscribers (non-blocking drop if full).
// StreamDelta/StreamThinking are debounced; other event types flush pending first.
func (h *Hub) Publish(sessionID, taskID, runID, parentRunID string, event providerapi.StreamEvent) {
	if h == nil {
		return
	}
	switch event.Type {
	case providerapi.StreamDelta, providerapi.StreamThinking:
		h.publishDebounced(sessionID, taskID, runID, parentRunID, event)
		return
	default:
		h.flushKey(h.batchKey(sessionID, runID))
		h.emit(sessionID, taskID, runID, parentRunID, event)
	}
}

// PublishTerminal flushes pending deltas for the run and emits StreamComplete.
// Call after durable run/task terminal writes so TUI refresh sees committed state.
func (h *Hub) PublishTerminal(sessionID, taskID, runID string) {
	if h == nil {
		return
	}
	h.flushKey(h.batchKey(sessionID, runID))
	h.emit(sessionID, taskID, runID, "", providerapi.StreamEvent{Type: providerapi.StreamComplete})
}

func (h *Hub) publishDebounced(sessionID, taskID, runID, parentRunID string, event providerapi.StreamEvent) {
	key := h.batchKey(sessionID, runID)
	debounce := h.Debounce
	if debounce <= 0 {
		debounce = DefaultDebounce
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	b := h.pending[key]
	if b == nil {
		b = &pendingBatch{sessionID: sessionID, taskID: taskID, runID: runID, parentRunID: parentRunID}
		h.pending[key] = b
	} else {
		if sessionID != "" {
			b.sessionID = sessionID
		}
		if taskID != "" {
			b.taskID = taskID
		}
		if runID != "" {
			b.runID = runID
		}
		if parentRunID != "" {
			b.parentRunID = parentRunID
		}
	}
	switch event.Type {
	case providerapi.StreamDelta:
		b.content += event.ContentDelta
	case providerapi.StreamThinking:
		b.thinking += event.ThinkingDelta
	}
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timer = time.AfterFunc(debounce, func() {
		h.flushKey(key)
	})
}

func (h *Hub) flushKey(key string) {
	h.mu.Lock()
	b, ok := h.pending[key]
	if !ok {
		h.mu.Unlock()
		return
	}
	delete(h.pending, key)
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	sessionID, taskID, runID, parentRunID := b.sessionID, b.taskID, b.runID, b.parentRunID
	content, thinking := b.content, b.thinking
	h.mu.Unlock()

	if content != "" {
		h.emit(sessionID, taskID, runID, parentRunID, providerapi.StreamEvent{
			Type: providerapi.StreamDelta, ContentDelta: content,
		})
	}
	if thinking != "" {
		h.emit(sessionID, taskID, runID, parentRunID, providerapi.StreamEvent{
			Type: providerapi.StreamThinking, ThinkingDelta: thinking,
		})
	}
}

func (h *Hub) emit(sessionID, taskID, runID, parentRunID string, event providerapi.StreamEvent) {
	env := Envelope{
		Seq:         h.seq.Add(1),
		SessionID:   sessionID,
		TaskID:      taskID,
		RunID:       runID,
		ParentRunID: parentRunID,
		Event:       event,
	}
	h.mu.Lock()
	subs := make([]*subscription, 0, len(h.subs))
	for _, sub := range h.subs {
		subs = append(subs, sub)
	}
	h.mu.Unlock()

	for _, sub := range subs {
		if sub.sessionID != "" && sessionID != "" && sub.sessionID != sessionID {
			continue
		}
		if sub.runID != "" && runID != "" && sub.runID != runID {
			continue
		}
		select {
		case sub.ch <- env:
		default:
			// Drop if subscriber is slow; TUI will reconcile via transcript.
		}
	}
}

// Handler returns a StreamHandler that publishes each event for the given run.
func (h *Hub) Handler(sessionID, taskID, runID string) providerapi.StreamHandler {
	if h == nil {
		return nil
	}
	return func(event providerapi.StreamEvent) error {
		h.Publish(sessionID, taskID, runID, "", event)
		return nil
	}
}

// Subscribe receives envelopes. Empty sessionID/runID matches all.
// Call the returned cancel to unsubscribe.
func (h *Hub) Subscribe(sessionID, runID string, buffer int) (<-chan Envelope, func()) {
	if h == nil {
		ch := make(chan Envelope)
		close(ch)
		return ch, func() {}
	}
	if buffer < 16 {
		buffer = 64
	}
	h.mu.Lock()
	h.nextSub++
	id := h.nextSub
	sub := &subscription{
		id: id, sessionID: sessionID, runID: runID,
		ch: make(chan Envelope, buffer),
	}
	h.subs[id] = sub
	h.mu.Unlock()
	cancel := func() {
		h.mu.Lock()
		if cur, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(cur.ch)
		}
		h.mu.Unlock()
	}
	return sub.ch, cancel
}
