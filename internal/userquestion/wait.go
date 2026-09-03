package userquestion

import (
	"context"
	"sync"
	"time"
)

type Decision struct {
	QuestionID string
	State      string
	Answers    map[string][]string
}

type Waiter struct {
	mu   sync.Mutex
	wait map[string]chan Decision
}

func NewWaiter() *Waiter {
	return &Waiter{wait: make(map[string]chan Decision)}
}

func (w *Waiter) Register(questionID string) {
	if w == nil || questionID == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.wait[questionID]; ok {
		return
	}
	w.wait[questionID] = make(chan Decision, 1)
}

func (w *Waiter) Wait(ctx context.Context, questionID string) (Decision, error) {
	if w == nil {
		return Decision{}, context.Canceled
	}
	w.mu.Lock()
	ch, ok := w.wait[questionID]
	if !ok {
		ch = make(chan Decision, 1)
		w.wait[questionID] = ch
	}
	w.mu.Unlock()
	select {
	case <-ctx.Done():
		w.cleanup(questionID)
		return Decision{}, ctx.Err()
	case d := <-ch:
		w.cleanup(questionID)
		return d, nil
	}
}

func (w *Waiter) Notify(d Decision) {
	if w == nil || d.QuestionID == "" {
		return
	}
	w.mu.Lock()
	ch, ok := w.wait[d.QuestionID]
	w.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- d:
	default:
	}
}

func (w *Waiter) cleanup(questionID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.wait, questionID)
}

const WaitTimeout = 30 * time.Minute
