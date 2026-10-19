package agent

import (
	"context"

	"github.com/yyZe0122/yunmengze-agent/internal/session"
)

func (e *Engine) ListSessions(ctx context.Context, workspace string) ([]session.Session, error) {
	return e.store.List(ctx, workspace)
}

func (e *Engine) GetSession(ctx context.Context, id string) (session.Session, error) {
	return e.store.Get(ctx, id)
}

func (e *Engine) SessionRecords(ctx context.Context, id string) ([]session.Record, error) {
	return e.store.Records(ctx, id, 0)
}

func (e *Engine) ConfigureSession(ctx context.Context, id string, mode session.Mode, model string) error {
	if err := e.ValidateModel(model); err != nil {
		return err
	}
	return e.store.Configure(ctx, id, mode, model)
}

func (e *Engine) ValidateModel(ref string) error {
	_, err := e.resolve(ref)
	return err
}
