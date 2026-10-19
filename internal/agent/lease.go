package agent

import (
	"context"
	"time"
)

// acquireSession keeps exclusive ownership for the entire operation, including
// slow provider requests, and joins renewal before releasing the lease.
func (e *Engine) acquireSession(ctx context.Context, id string) (context.Context, func(), error) {
	token, err := e.store.Acquire(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e.store.Renew(ctx, id, token) != nil {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() {
		cancel()
		<-done
		cleanup, finish := context.WithTimeout(context.Background(), 3*time.Second)
		defer finish()
		_ = e.store.Release(cleanup, id, token)
	}, nil
}
