package userquestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/applicationerror"
	"github.com/yyZe0122/yunmengze-agent/internal/events"
	"github.com/yyZe0122/yunmengze-agent/pkg/eventapi"
)

type Config struct {
	Store  *Store
	Waiter *Waiter
	Events *events.Store
	Now    func() time.Time
}

type Service struct {
	store  *Store
	waiter *Waiter
	events *events.Store
	now    func() time.Time
}

func New(config Config) (*Service, error) {
	if config.Store == nil {
		return nil, errors.New("userquestion store is required")
	}
	waiter := config.Waiter
	if waiter == nil {
		waiter = NewWaiter()
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: config.Store, waiter: waiter, events: config.Events, now: now}, nil
}

func (s *Service) Waiter() *Waiter {
	if s == nil {
		return nil
	}
	return s.waiter
}

func (s *Service) ListPending(ctx context.Context, sessionID string, limit int) ([]Request, error) {
	return s.store.ListPending(ctx, sessionID, limit)
}

func (s *Service) Get(ctx context.Context, id string) (Request, error) {
	return s.store.Get(ctx, id)
}

func NewID(sessionID, toolCallID string, now time.Time) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(sessionID) + "\x00" + strings.TrimSpace(toolCallID) + "\x00" + now.UTC().Format(time.RFC3339Nano)))
	return "uq-" + hex.EncodeToString(h[:8])
}

func (s *Service) CreatePending(ctx context.Context, req Request) (Request, error) {
	if s == nil {
		return Request{}, errors.New("userquestion service is nil")
	}
	if err := validateItems(req.Questions); err != nil {
		return Request{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		req.ID = NewID(req.SessionID, req.ToolCallID, s.now())
	}
	req.State = StatePending
	req.CreatedAt = s.now().UTC().Format(time.RFC3339Nano)
	if err := s.store.Insert(ctx, req); err != nil {
		return Request{}, err
	}
	s.waiter.Register(req.ID)
	s.emit(ctx, "question.pending", req)
	return req, nil
}

func (s *Service) Answer(ctx context.Context, id, actor string, answers map[string][]string) (Request, error) {
	if s == nil {
		return Request{}, errors.New("userquestion service is nil")
	}
	id = strings.TrimSpace(id)
	req, err := s.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Request{}, applicationerror.Wrap(applicationerror.CodeNotFound, false, err)
		}
		return Request{}, err
	}
	if req.State != StatePending {
		return Request{}, applicationerror.Wrap(applicationerror.CodeConflict, false, ErrNotPending)
	}
	if err := validateAnswers(req.Questions, answers); err != nil {
		return Request{}, applicationerror.Wrap(applicationerror.CodeInvalidRequest, false, err)
	}
	decidedAt := s.now().UTC().Format(time.RFC3339Nano)
	if actor == "" {
		actor = "local-user"
	}
	if err := s.store.MarkDecided(ctx, id, StateAnswered, actor, decidedAt, answers); err != nil {
		return Request{}, err
	}
	req.State = StateAnswered
	req.Answers = answers
	req.DecidedBy = actor
	req.DecidedAt = decidedAt
	s.waiter.Notify(Decision{QuestionID: id, State: StateAnswered, Answers: answers})
	s.emit(ctx, "question.answered", req)
	return req, nil
}

func (s *Service) Dismiss(ctx context.Context, id, actor string) (Request, error) {
	if strings.TrimSpace(actor) == "" {
		actor = "local-user"
	}
	return s.markUnavailable(ctx, id, actor, "user dismissed")
}

func (s *Service) MarkUnavailable(ctx context.Context, id, reason string) (Request, error) {
	return s.markUnavailable(ctx, id, "system", reason)
}

func (s *Service) markUnavailable(ctx context.Context, id, actor, reason string) (Request, error) {
	if s == nil {
		return Request{}, errors.New("userquestion service is nil")
	}
	req, err := s.store.Get(ctx, id)
	if err != nil {
		return Request{}, err
	}
	if req.State != StatePending {
		return req, nil
	}
	decidedAt := s.now().UTC().Format(time.RFC3339Nano)
	answers := map[string][]string{}
	if reason != "" {
		answers["_error"] = []string{reason}
	}
	if strings.TrimSpace(actor) == "" {
		actor = "system"
	}
	if err := s.store.MarkDecided(ctx, id, StateUnavailable, actor, decidedAt, answers); err != nil {
		return Request{}, err
	}
	req.State = StateUnavailable
	req.Answers = answers
	req.DecidedBy = actor
	req.DecidedAt = decidedAt
	s.waiter.Notify(Decision{QuestionID: id, State: StateUnavailable, Answers: answers})
	s.emit(ctx, "question.answered", req)
	return req, nil
}

func validateItems(items []Item) error {
	if len(items) == 0 {
		return fmt.Errorf("%w: at least one question is required", ErrInvalid)
	}
	if len(items) > 8 {
		return fmt.Errorf("%w: at most 8 questions", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(items))
	for i, item := range items {
		id := strings.TrimSpace(item.ID)
		q := strings.TrimSpace(item.Question)
		if id == "" || q == "" {
			return fmt.Errorf("%w: questions[%d] needs id and question", ErrInvalid, i)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("%w: duplicate question id %q", ErrInvalid, id)
		}
		seen[id] = struct{}{}
		if len(item.Options) > 12 {
			return fmt.Errorf("%w: questions[%d] has too many options", ErrInvalid, i)
		}
		for j, opt := range item.Options {
			if strings.TrimSpace(opt.Label) == "" {
				return fmt.Errorf("%w: questions[%d].options[%d] needs a label", ErrInvalid, i, j)
			}
		}
	}
	return nil
}

func validateAnswers(items []Item, answers map[string][]string) error {
	if answers == nil {
		return fmt.Errorf("%w: answers are required", ErrInvalid)
	}
	byID := make(map[string]Item, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	for id, chosen := range answers {
		item, ok := byID[id]
		if !ok {
			return fmt.Errorf("%w: unknown question id %q", ErrInvalid, id)
		}
		if len(chosen) == 0 {
			return fmt.Errorf("%w: answers for %q are empty", ErrInvalid, id)
		}
		if !item.MultiSelect && len(chosen) > 1 {
			return fmt.Errorf("%w: question %q is single-select", ErrInvalid, id)
		}
		for _, label := range chosen {
			if strings.TrimSpace(label) == "" {
				return fmt.Errorf("%w: answers for %q are empty", ErrInvalid, id)
			}
		}
	}
	for _, item := range items {
		if _, ok := answers[item.ID]; !ok {
			return fmt.Errorf("%w: missing answer for %q", ErrInvalid, item.ID)
		}
	}
	return nil
}

func (s *Service) emit(ctx context.Context, typ string, req Request) {
	if s == nil || s.events == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	payload, err := json.Marshal(map[string]any{
		"question_id": req.ID,
		"session_id":  req.SessionID,
		"task_id":     req.TaskID,
		"run_id":      req.RunID,
		"state":       req.State,
	})
	if err != nil {
		return
	}
	aggID := req.SessionID
	if aggID == "" {
		aggID = req.ID
	}
	_, _ = s.events.Append(ctx, eventapi.Envelope{
		ID:               fmt.Sprintf("question/%s/%s/%d", typ, req.ID, s.now().UnixNano()),
		Type:             typ,
		AggregateType:    "user_question",
		AggregateID:      aggID,
		AggregateVersion: 1,
		OccurredAt:       s.now().UTC(),
		Producer:         "userquestion",
		SchemaVersion:    1,
		Payload:          payload,
	})
}
