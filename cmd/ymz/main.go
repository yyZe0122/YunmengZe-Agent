package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/yyZe0122/yunmengze-agent/internal/agent"
	"github.com/yyZe0122/yunmengze-agent/internal/modelcatalog"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/session"
	"github.com/yyZe0122/yunmengze-agent/internal/store/sqlite"
	"github.com/yyZe0122/yunmengze-agent/internal/tui"
	"github.com/yyZe0122/yunmengze-agent/internal/version"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ymz:", err)
		os.Exit(1)
	}
}
func homeDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("YMZ_HOME")); dir != "" {
		return filepath.Abs(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".yunmengze"), nil
}

const usage = `ymz — local coding agent

  ymz [--mode agent|plan] [--model provider/model] [--session ID|--continue]
  ymz run [same flags] [--json] "objective"
  ymz sessions
  ymz models [refresh]
  ymz config validate
  ymz db check
  ymz paths
  ymz version

Configuration: $YMZ_HOME or ~/.yunmengze. No daemon is required.
agent executes tools directly; plan is read-only. /help lists TUI commands.
`

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	command := "tui"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
	}
	if command == "help" || len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprint(out, usage)
		return err
	}
	if command == "version" {
		_, err := fmt.Fprintf(out, "ymz %s (%s, %s)\n", version.Version, version.Commit, version.Date)
		return err
	}
	dir, err := homeDir()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	switch command {
	case "paths":
		return json.NewEncoder(out).Encode(map[string]string{"config": dir, "database": filepath.Join(dir, "core.db"), "workspace": cwd})
	case "config":
		if len(args) != 1 || args[0] != "validate" {
			return errors.New("usage: ymz config validate")
		}
		if _, err = providerconfig.EnsureConfig(dir); err != nil {
			return err
		}
		modelcatalog.Init(dir)
		snapshot, err := providerconfig.LoadSnapshot(dir)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "configuration valid for enabled features")
		for _, model := range snapshot.Models {
			if model.Unavailable != "" {
				fmt.Fprintf(errOut, "%s unavailable: %s\n", model.ID, model.Unavailable)
			}
		}
		return nil
	case "models":
		if len(args) > 1 || len(args) == 1 && args[0] != "refresh" {
			return errors.New("usage: ymz models [refresh]")
		}
		if len(args) == 1 {
			if err = modelcatalog.Refresh(ctx, dir); err != nil {
				return err
			}
		}
		_, items, err := providerconfig.ListModelRefs(dir)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(items)
	case "sessions", "db":
		if command == "db" && (len(args) != 1 || args[0] != "check") {
			return errors.New("usage: ymz db check")
		}
		if command == "sessions" && len(args) != 0 {
			return errors.New("usage: ymz sessions")
		}
		db, err := sqlite.Open(ctx, filepath.Join(dir, "core.db"))
		if err != nil {
			return err
		}
		defer db.Close()
		if command == "db" {
			if err = db.Health(ctx); err != nil {
				return err
			}
			fmt.Fprintln(out, "database OK")
			return nil
		}
		items, err := session.New(db.SQL()).List(ctx, cwd)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(items)
	case "tui", "run":
	default:
		return fmt.Errorf("unknown command %q; use ymz help", command)
	}
	flags := flag.NewFlagSet("ymz "+command, flag.ContinueOnError)
	flags.SetOutput(errOut)
	modeFlag := flags.String("mode", "agent", "agent or plan")
	modelFlag := flags.String("model", "", "provider/model")
	id := flags.String("session", "", "saved session ID")
	resume := flags.Bool("continue", false, "continue the latest session in this workspace")
	jsonMode := flags.Bool("json", false, "emit JSON events")
	if err = flags.Parse(args); err != nil {
		return err
	}
	mode, err := session.ParseMode(*modeFlag)
	if err != nil {
		return err
	}
	if *id != "" && *resume {
		return errors.New("use only one of --session and --continue")
	}
	input := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if command == "run" && input == "" {
		return errors.New("usage: ymz run [flags] objective")
	}
	if command == "tui" && flags.NArg() != 0 {
		return errors.New("use ymz run for a command-line objective")
	}
	engine, err := agent.Open(ctx, dir, cwd)
	if err != nil {
		return err
	}
	defer engine.Close()
	var s session.Session
	if *resume {
		items, err := engine.ListSessions(ctx, cwd)
		if err != nil {
			return err
		}
		if len(items) > 0 {
			*id = items[0].ID
		}
	}
	explicitMode := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "mode" {
			explicitMode = true
		}
	})
	if *id != "" {
		s, err = engine.GetSession(ctx, *id)
		if err != nil {
			return err
		}
		if s.ParentID != "" {
			return errors.New("child session must be resumed through task")
		}
		if !explicitMode {
			mode = s.Mode
		}
		model := s.Model
		if *modelFlag != "" {
			model = *modelFlag
		}
		s.Mode = mode
		if err = engine.SetModel(ctx, s, model); err != nil {
			return err
		}
		s.Model = model
	}
	ref := *modelFlag
	if ref == "" {
		ref = engine.Main
	}
	if s.ID != "" {
		ref = s.Model
	}
	if command == "tui" {
		logFile, err := os.OpenFile(filepath.Join(dir, "ymz.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer logFile.Close()
		previous := slog.Default()
		slog.SetDefault(slog.New(slog.NewJSONHandler(logFile, nil)))
		defer slog.SetDefault(previous)
		return tui.Run(ctx, engine, s, mode, ref)
	}
	if s.ID == "" {
		s, err = engine.NewSession(ctx, mode, ref)
		if err != nil {
			return err
		}
	}

	encoder := json.NewEncoder(out)
	if *jsonMode {
		if err = encoder.Encode(map[string]string{"type": "session", "id": s.ID}); err != nil {
			return err
		}
	}
	var writeErr error
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	_, err = engine.Run(runCtx, s.ID, input, agent.RunOptions{Emit: func(event agent.Event) {
		if writeErr != nil {
			return
		}
		if *jsonMode {
			writeErr = encoder.Encode(event)
		} else if event.Type == "delta" && event.TaskID == "" {
			_, writeErr = fmt.Fprint(out, event.Text)
		} else if event.Type == "tool_start" {
			_, writeErr = fmt.Fprintf(errOut, "[%s]\n", event.Tool)
		} else if event.Type == "info" {
			_, writeErr = fmt.Fprintln(errOut, event.Text)
		}
		if writeErr != nil {
			cancelRun()
		}
	}})
	if writeErr != nil {
		return writeErr
	}
	if !*jsonMode {
		_, writeErr = fmt.Fprintln(out)
	} else {
		final := map[string]string{"type": "done", "session_id": s.ID}
		if err != nil {
			final["error"] = err.Error()
		}
		writeErr = encoder.Encode(final)
	}
	if writeErr != nil {
		return writeErr
	}
	return err
}
