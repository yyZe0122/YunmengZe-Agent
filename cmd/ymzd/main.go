package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/configreload"
	"github.com/yyZe0122/yunmengze-agent/internal/daemonctl"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
	platformsignals "github.com/yyZe0122/yunmengze-agent/internal/platform/signals"
	"github.com/yyZe0122/yunmengze-agent/internal/version"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(os.Args[1:]); err != nil {
		slog.Error("daemon stopped", "component", "daemon", "operation", "run", "result", "failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("ymzd", flag.ContinueOnError)
	modeValue := flags.String("mode", string(paths.ModeUser), "runtime mode: user or system")
	logLevelValue := flags.String("log-level", defaultLogLevel(), "log level: debug, info, warn, or error")
	check := flags.Bool("check", false, "validate core bootstrap and print status")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Printf("ymzd %s commit=%s date=%s\n", version.Version, version.Commit, version.Date)
		return nil
	}

	mode, err := paths.ParseMode(*modeValue)
	if err != nil {
		return err
	}
	layout, err := paths.Resolve(mode)
	if err != nil {
		return err
	}
	logLevel, err := parseLogLevel(*logLevelValue)
	if err != nil {
		return err
	}
	if _, err := configureLogging(layout.LogDir, logLevel, string(mode)); err != nil {
		return err
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	skillCatalog, skillDiagnostics := discoverSkillCatalog(layout, workingDirectory)
	for _, diagnostic := range skillDiagnostics {
		slog.Warn("skill discovery diagnostic", "component", "skills", "operation", "discover", "result", "warning", "error", diagnostic)
	}

	stores, err := openCoreStores(context.Background(), layout.DataDir)
	if err != nil {
		return err
	}
	defer stores.database.Close()

	stack, err := wireTools(stores, layout, workingDirectory)
	if err != nil {
		return err
	}
	if stack.mcpRegistry != nil {
		defer stack.mcpRegistry.Close()
	}

	chat, err := wireChat(stores, stack, layout, workingDirectory)
	if err != nil {
		return err
	}
	if chat.memoryManager != nil {
		defer func() { _ = chat.memoryManager.Shutdown(context.Background()) }()
	}

	taskSubmissionService, taskControl, skillMaintain, schedulerStore, core, err := wireAppAndJobs(
		stores, stack, chat, skillCatalog, layout, workingDirectory,
	)
	if err != nil {
		return err
	}
	if *check {
		if err := chat.queries.Check(context.Background()); err != nil {
			return fmt.Errorf("check core database: %w", err)
		}
		if err := schedulerStore.Ping(context.Background()); err != nil {
			return fmt.Errorf("check scheduler store: %w", err)
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(core.Status()); err != nil {
			return err
		}
		slog.Info("daemon check completed", "component", "daemon", "operation", "check", "result", "succeeded")
		return nil
	}

	gatewayAPI, err := wireGatewayAPI(
		stores, stack, chat, layout, skillCatalog,
		taskSubmissionService, taskControl, skillMaintain, schedulerStore, core,
	)
	if err != nil {
		return err
	}
	bindProviderGateway(chat, gatewayAPI)
	gatewayRunner, err := newGatewayRunner(layout, gatewayAPI)
	if err != nil {
		return err
	}
	defer gatewayRunner.Close()
	if err := daemonctl.WritePID(layout.RuntimeDir); err != nil {
		return err
	}
	defer func() {
		if err := daemonctl.RemovePID(layout.RuntimeDir); err != nil {
			slog.Warn("remove daemon pid file", "component", "daemon", "operation", "stop", "result", "warning", "error", err)
		}
	}()
	if err := core.AddBackgroundRunner(gatewayRunner); err != nil {
		return err
	}

	status := core.Status()
	slog.Info("daemon started", "component", "daemon", "operation", "start", "result", "succeeded", "version", status.Version, "mode", status.Runtime.Mode, "workspace_root", workingDirectory)
	ctx, cancel := platformsignals.NotifyContext(context.Background())
	defer cancel()

	if chat.providerRuntime != nil {
		chat.providerRuntime.StartCatalogRefresh(ctx)
		chat.providerRuntime.NoteFingerprint()
		if w, werr := configreload.New(configreload.Options{
			ConfigDir: layout.ConfigDir,
			Debounce:  500 * time.Millisecond,
			OnChange: func() {
				if err := chat.providerRuntime.ReloadFromDisk(); err != nil {
					slog.Warn("provider config reload failed", "component", "configreload", "operation", "reload", "result", "warning", "error", err)
				}
			},
			Logger: slog.Default(),
		}); werr != nil {
			slog.Warn("provider config watch not started", "component", "configreload", "operation", "watch", "result", "warning", "error", werr)
		} else {
			if err := w.Start(ctx); err != nil {
				slog.Warn("provider config watch start failed", "component", "configreload", "operation", "watch", "result", "warning", "error", err)
				_ = w.Close()
			} else {
				defer func() { _ = w.Close() }()
				slog.Info("provider config watch started", "component", "configreload", "operation", "watch", "result", "succeeded", "config_dir", layout.ConfigDir)
			}
		}
	}

	if err := core.Run(ctx); err != nil {
		return err
	}
	slog.Info("daemon stopped", "component", "daemon", "operation", "run", "result", "succeeded")
	return nil
}
