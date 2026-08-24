package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-isatty"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
	"github.com/yyZe0122/yunmengze-agent/internal/modelstream"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
	"github.com/yyZe0122/yunmengze-agent/pkg/eventapi"
)

// Config configures the local TUI.
type Config struct {
	Mode    paths.Mode
	Gateway Gateway // optional; production defaults to gatewayclient.New(Mode)
}

// Run starts the bubbletea program. Requires a TTY on stdin.
func Run(config Config) error {
	if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		return errors.New("tui requires an interactive terminal")
	}
	gw := config.Gateway
	if gw == nil {
		client, err := gatewayclient.New(config.Mode)
		if err != nil {
			return err
		}
		gw = client
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := newModel(config.Mode, gw)
	program := tea.NewProgram(m, tea.WithContext(ctx))

	go streamSSE(ctx, gw, program)
	go streamModel(ctx, gw, program)

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}

func streamSSE(ctx context.Context, gw Gateway, program *tea.Program) {
	var after uint64
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		program.Send(sseStateMsg{state: "ok"})
		err := gw.StreamEvents(ctx, after, func(envelope eventapi.Envelope) error {
			if envelope.Sequence > after {
				after = envelope.Sequence
			}
			program.Send(sseEventMsg{envelope: envelope})
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		program.Send(sseStateMsg{state: "reconnecting", err: err})
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
		if err == nil {
			backoff = time.Second
		}
	}
}

func streamModel(ctx context.Context, gw Gateway, program *tea.Program) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		// Subscribe to all sessions; TUI filters by focused sessionID.
		err := gw.StreamModelEvents(ctx, "", "", func(env modelstream.Envelope) error {
			program.Send(modelStreamMsg{env: env})
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err == nil {
			backoff = time.Second
		} else if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}
