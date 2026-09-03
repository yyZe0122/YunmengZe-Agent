package gateway

import (
	"context"

	"github.com/yyZe0122/yunmengze-agent/internal/userquestion"
)

type UserQuestionAdapter struct {
	Service *userquestion.Service
}

func (a UserQuestionAdapter) ListPending(ctx context.Context, sessionID string, limit int) ([]UserQuestionView, error) {
	if a.Service == nil {
		return []UserQuestionView{}, nil
	}
	items, err := a.Service.ListPending(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]UserQuestionView, 0, len(items))
	for _, item := range items {
		out = append(out, questionView(item))
	}
	return out, nil
}

func (a UserQuestionAdapter) Answer(ctx context.Context, id, actor string, answers map[string][]string) (UserQuestionView, error) {
	if a.Service == nil {
		return UserQuestionView{}, userquestion.ErrNotFound
	}
	item, err := a.Service.Answer(ctx, id, actor, answers)
	if err != nil {
		return UserQuestionView{}, err
	}
	return questionView(item), nil
}

func (a UserQuestionAdapter) Dismiss(ctx context.Context, id, actor string) (UserQuestionView, error) {
	if a.Service == nil {
		return UserQuestionView{}, userquestion.ErrNotFound
	}
	item, err := a.Service.Dismiss(ctx, id, actor)
	if err != nil {
		return UserQuestionView{}, err
	}
	return questionView(item), nil
}

func questionView(item userquestion.Request) UserQuestionView {
	qs := make([]UserQuestionItem, 0, len(item.Questions))
	for _, q := range item.Questions {
		opts := make([]UserQuestionOption, 0, len(q.Options))
		for _, opt := range q.Options {
			opts = append(opts, UserQuestionOption{Label: opt.Label, Description: opt.Description})
		}
		qs = append(qs, UserQuestionItem{
			ID: q.ID, Question: q.Question, Header: q.Header, Options: opts, MultiSelect: q.MultiSelect,
		})
	}
	return UserQuestionView{
		ID: item.ID, SessionID: item.SessionID, TaskID: item.TaskID, RunID: item.RunID,
		ToolCallID: item.ToolCallID, Questions: qs, State: item.State, Answers: item.Answers,
		CreatedAt: item.CreatedAt, DecidedAt: item.DecidedAt,
	}
}
