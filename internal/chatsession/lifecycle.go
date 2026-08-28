package chatsession

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"

	"github.com/yyZe0122/yunmengze-agent/internal/agent"
	"github.com/yyZe0122/yunmengze-agent/internal/applicationerror"
	"github.com/yyZe0122/yunmengze-agent/internal/approval"
	"github.com/yyZe0122/yunmengze-agent/internal/audit"
	"github.com/yyZe0122/yunmengze-agent/internal/contextpack"
	"github.com/yyZe0122/yunmengze-agent/internal/corequery"
	"github.com/yyZe0122/yunmengze-agent/internal/injectscan"
	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	"github.com/yyZe0122/yunmengze-agent/internal/memory"
	"github.com/yyZe0122/yunmengze-agent/internal/runlog"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func (s *Service) executeChat(
	task kernel.Task,
	plan approval.PlanDocument,
	planHash string,
	runID kernel.RunID,
	grantIDs map[string][]string,
	userText string,
	modelRef string,
	actor string,
	interactive bool,
) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(plan.Budget.MaxDurationMillis)*time.Millisecond)
	defer cancel()
	s.setActive(task.ID, task.SessionID, cancel)
	defer func() {
		s.clearActive(task.ID)
		if s.sessionHasActive(task.SessionID) {
			return
		}
		if s.agent != nil {
			if inbox := s.agent.Inbox(); inbox != nil {
				inbox.Clear(string(task.SessionID))
			}
		}
	}()

	stepID := planChatStepID(plan)
	// Mark run running.
	now := s.now().UTC()
	_, _ = s.db.ExecContext(ctx, `
		UPDATE runs SET state = ?, version = version + 1, updated_at = ? WHERE run_id = ? AND state = ?`,
		kernel.RunRunning, formatTime(now), runID, kernel.RunCreated,
	)
	_, _ = s.db.ExecContext(ctx, `
		UPDATE plan_steps SET state = ?, version = version + 1, updated_at = ? WHERE step_id = ? AND plan_id = ?`,
		kernel.StepRunning, formatTime(now), stepID, plan.PlanID,
	)

	allowed := uniqueCapabilityNames(plan.Steps[0].Capabilities)
	timeoutMS := plan.Steps[0].TimeoutMillis
	for _, cap := range plan.Steps[0].Capabilities {
		if cap.MaxDurationMillis > 0 && cap.MaxDurationMillis < timeoutMS {
			timeoutMS = cap.MaxDurationMillis
		}
	}

	// H7 job pin > O4 session prefer > daemon main. Pin before Prefix env + Build (ADR-051).
	pin := s.resolveRunModelPin(ctx, task.SessionID, modelRef)
	window := s.contextWindow
	reqMax := s.maxOutputTokens
	modelID := ""
	modelLabel := strings.TrimSpace(s.mainModel)
	if pin != nil {
		if pin.ContextWindow > 0 {
			window = pin.ContextWindow
		}
		if pin.MaxTokens > 0 {
			reqMax = pin.MaxTokens
		}
		modelID = pin.Model
		if ref := strings.TrimSpace(pin.Ref); ref != "" {
			modelLabel = ref
		} else if pin.Model != "" {
			modelLabel = pin.Model
		}
		src := "session"
		if strings.TrimSpace(modelRef) != "" {
			src = "job"
		}
		slog.Info("chat run using model pin", runlog.Attrs("chatsession", "execute", "model_pin", runlog.IDs{
			SessionID: string(task.SessionID), TaskID: string(task.ID), RunID: string(runID),
		}, "source", src, "pin", pin.Ref, "model", pin.Model)...)
	}

	sysPrompt := chatSystemPrompt(kernel.NormalizeExecutionMode(string(task.ExecutionMode)) == kernel.ExecutionModePlan, interactive, s.configuredRoles)
	prefix := []providerapi.Message{
		{Role: providerapi.RoleSystem, Content: sysPrompt},
		{Role: providerapi.RoleSystem, Content: chatEnvBlock(modelLabel, s.sessionWorkspace(ctx, task), s.now().UTC().Format("2006-01-02"))},
	}
	if agentsMsg := s.agentsSystemMessage(ctx, task); agentsMsg != nil {
		prefix = append(prefix, *agentsMsg)
	}
	if catalogMsg := s.skillCatalogMessage(); catalogMsg != nil {
		prefix = append(prefix, *catalogMsg)
	}
	if skillMsg := s.skillSystemMessage(ctx, task.ID); skillMsg != nil {
		prefix = append(prefix, *skillMsg)
	}
	if s.memory != nil {
		block := s.memory.FrozenSystemBlock(ctx, string(task.SessionID))
		if block != "" {
			prefix = append(prefix, providerapi.Message{Role: providerapi.RoleSystem, Content: block})
		}
	}
	if window <= 0 {
		window = contextpack.DefaultContextWindow
	}
	packMax := contextpack.ClampMaxOutput(reqMax)
	view, err := s.buildContextView(ctx, task.SessionID, task.ID, userText, prefix, window, packMax, modelID)
	if err != nil {
		s.failChat(context.WithoutCancel(ctx), task, runID, stepID, err)
		s.onError(err)
		return
	}
	persist := append(append([]providerapi.Message(nil), prefix...), providerapi.Message{Role: providerapi.RoleUser, Content: userText})
	runActor := strings.TrimSpace(actor)
	if runActor == "" {
		runActor = "local-user"
	}
	if strings.HasPrefix(string(task.ID), "scheduled_") {
		runActor = "scheduler"
	}
	runReq := agent.RunRequest{
		RunID: string(runID), TaskID: string(task.ID), SessionID: string(task.SessionID),
		PlanID: string(plan.PlanID), PlanHash: planHash, StepID: string(stepID),
		Actor: runActor, TraceID: string(runID), Interactive: interactive,
		Messages: persist, ProviderMessages: view.Messages(),
		AllowedTools: allowed, CapabilityGrantIDs: grantIDs,
		MaxOutputTokens: reqMax, MaxTotalTokens: plan.Budget.MaxTokens,
		MaxCostMicros: plan.Budget.MaxCostMicros, ToolTimeoutMillis: timeoutMS,
		ContextWindow: window,
		Compacted:     view.Compacted,
	}
	if pin != nil {
		runReq.ModelOverride = pin.Model
		runReq.OverrideProvider = pin.Provider
		runReq.OverrideContextWindow = pin.ContextWindow
		if pin.MaxTokens > 0 {
			runReq.OverrideMaxOutputTokens = pin.MaxTokens
		}
	}
	result, err := s.agent.Run(ctx, runReq)
	if err != nil {
		bg := context.WithoutCancel(ctx)
		if errors.Is(err, context.Canceled) {
			state, stateErr := s.taskState(bg, task.ID)
			if stateErr == nil && (state == kernel.TaskPaused || state == kernel.TaskCancelled) {
				if state == kernel.TaskCancelled {
					s.cancelChat(bg, task, runID, stepID)
				}
				// pause: leave run/step running so resume/inspect remain possible.
				slog.Info("chat run interrupted", runlog.Attrs("chatsession", "execute", string(state), runlog.IDs{
					SessionID: string(task.SessionID), TaskID: string(task.ID), RunID: string(runID),
					PlanID: string(plan.PlanID), StepID: string(stepID),
				})...)
				return
			}
			if stateErr != nil {
				s.failChat(bg, task, runID, stepID, errors.Join(err, stateErr))
				s.onError(err)
				return
			}
			// Context canceled without control transition (e.g. budget timeout race): treat as fail.
		}
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("chat execution budget exceeded: %w", err)
		}
		s.failChat(bg, task, runID, stepID, err)
		s.onError(err)
		return
	}
	if err := s.completeChat(context.WithoutCancel(ctx), task, runID, stepID, result); err != nil {
		s.onError(err)
		return
	}
	if s.memory != nil {
		bg := context.WithoutCancel(ctx)
		s.memory.SyncTurn(bg, string(task.SessionID), userText, result.Content)
		// L3 transcript projection for session_search (user + assistant only).
		nowRFC := formatTime(s.now())
		_ = s.memory.IndexTranscriptRecord(bg, string(task.SessionID), string(runID), 0, "user", userText, nowRFC)
		if strings.TrimSpace(result.Content) != "" {
			_ = s.memory.IndexTranscriptRecord(bg, string(task.SessionID), string(runID), 1, "assistant", result.Content, nowRFC)
		}
		// H1-lite: async LLM curator (does not touch frozen system block).
		s.runMemoryCurator(bg, string(task.SessionID), userText, result.Content)
		s.memory.Maintain(bg)
	}
}

func (s *Service) runMemoryCurator(ctx context.Context, sessionID, userText, assistantText string) {
	if s == nil || s.memory == nil || s.curatorCaller == nil {
		return
	}
	enabled := true
	maxFacts := 3
	timeoutMS := 15_000
	if s.chatCfg != nil {
		enabled = s.chatCfg.MemoryCuratorEnabled()
		maxFacts = s.chatCfg.MemoryCuratorMaxFacts()
		timeoutMS = s.chatCfg.MemoryCuratorTimeoutMS()
	}
	if !enabled {
		return
	}
	go s.memory.CurateTurn(ctx, sessionID, userText, assistantText, memory.CuratorConfig{
		Enabled: true, MaxFacts: maxFacts, TimeoutMS: timeoutMS, Caller: s.curatorCaller,
	})
}

// RefreshMemory invalidates the frozen system memory snapshot for a session
// (empty sessionID clears all). Next chat turn rebuilds the inject block.
func (s *Service) completeChat(ctx context.Context, task kernel.Task, runID kernel.RunID, stepID kernel.StepID, result agent.Result) error {
	taskID := task.ID
	// Control may have paused/cancelled while the model was finishing.
	if state, err := s.taskState(ctx, taskID); err == nil {
		if state == kernel.TaskCancelled {
			s.cancelChat(ctx, task, runID, stepID)
			return nil
		}
		if state == kernel.TaskPaused {
			slog.Info("chat run finished after pause", runlog.Attrs("chatsession", "execute", "paused", runlog.IDs{
				SessionID: string(task.SessionID), TaskID: string(taskID), RunID: string(runID),
			})...)
			return nil
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE runs SET state = ?, version = version + 1, updated_at = ?, finished_at = ?, error = NULL
		WHERE run_id = ? AND state IN (?, ?)`,
		kernel.RunCompleted, formatTime(now), formatTime(now), runID, kernel.RunRunning, kernel.RunCreated,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE plan_steps SET state = ?, version = version + 1, updated_at = ?
		WHERE step_id = ?`, kernel.StepCompleted, formatTime(now), stepID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE tasks SET state = ?, version = version + 1, updated_at = ?
		WHERE task_id = ? AND state IN (?, ?)`,
		kernel.TaskCompleted, formatTime(now), taskID, kernel.TaskRunning, kernel.TaskPaused,
	); err != nil {
		return err
	}
	if err := s.audit.RecordTx(ctx, tx, audit.Entry{
		OccurredAt: now, Actor: "agent", Action: "chat.execute", ResourceType: "run", ResourceID: string(runID),
		Outcome: "succeeded", TraceID: string(runID),
		Details: map[string]any{"task_id": taskID, "iterations": result.Iterations, "tool_calls": len(result.ToolCalls)},
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.publishTerminal(task, runID)
	slog.Info("chat run completed", runlog.Attrs("chatsession", "execute", "succeeded", runlog.IDs{
		SessionID: string(task.SessionID), TaskID: string(taskID), RunID: string(runID),
		StepID: string(stepID),
	}, "iterations", result.Iterations, "tool_calls", len(result.ToolCalls))...)
	return nil
}

// cancelChat marks the chat run finished after operator cancel. Task is already cancelled by taskcontrol.
// plan_steps has no cancelled terminal from running; leave step for inspection (run is authoritative).
func (s *Service) cancelChat(ctx context.Context, task kernel.Task, runID kernel.RunID, _ kernel.StepID) {
	taskID := task.ID
	s.cancelIncompleteTools(ctx, runID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	now := s.now().UTC()
	_, _ = tx.ExecContext(ctx, `
		UPDATE runs SET state = ?, version = version + 1, updated_at = ?, finished_at = ?, error = NULL
		WHERE run_id = ? AND state IN (?, ?)`,
		kernel.RunCancelled, formatTime(now), formatTime(now), runID, kernel.RunRunning, kernel.RunCreated,
	)
	_ = s.audit.RecordTx(ctx, tx, audit.Entry{
		OccurredAt: now, Actor: "agent", Action: "chat.execute", ResourceType: "run", ResourceID: string(runID),
		Outcome: "cancelled", TraceID: string(runID), Details: map[string]any{"task_id": taskID},
	})
	_ = tx.Commit()
	s.publishTerminal(task, runID)
	slog.Info("chat run cancelled", runlog.Attrs("chatsession", "execute", "cancelled", runlog.IDs{
		SessionID: string(task.SessionID), TaskID: string(taskID), RunID: string(runID),
	})...)
}

func (s *Service) failChat(ctx context.Context, task kernel.Task, runID kernel.RunID, stepID kernel.StepID, cause error) {
	taskID := task.ID
	failure := strings.TrimSpace(cause.Error())
	if len(failure) > 2048 {
		failure = failure[:2048]
	}
	s.cancelIncompleteTools(ctx, runID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	now := s.now().UTC()
	_, _ = tx.ExecContext(ctx, `
		UPDATE runs SET state = ?, version = version + 1, updated_at = ?, finished_at = ?, error = ?
		WHERE run_id = ?`, kernel.RunFailed, formatTime(now), formatTime(now), failure, runID)
	_, _ = tx.ExecContext(ctx, `
		UPDATE plan_steps SET state = ?, version = version + 1, updated_at = ? WHERE step_id = ?`,
		kernel.StepFailed, formatTime(now), stepID)
	_, _ = tx.ExecContext(ctx, `
		UPDATE tasks SET state = ?, version = version + 1, updated_at = ?
		WHERE task_id = ? AND state IN (?, ?)`,
		kernel.TaskFailed, formatTime(now), taskID, kernel.TaskRunning, kernel.TaskPaused)
	_ = s.audit.RecordTx(ctx, tx, audit.Entry{
		OccurredAt: now, Actor: "agent", Action: "chat.execute", ResourceType: "run", ResourceID: string(runID),
		Outcome: "failed", TraceID: string(runID), Details: map[string]any{"task_id": taskID, "error": failure},
	})
	_ = tx.Commit()
	s.publishTerminal(task, runID)
	slog.Error("chat run failed", runlog.Attrs("chatsession", "execute", "failed", runlog.IDs{
		SessionID: string(task.SessionID), TaskID: string(taskID), RunID: string(runID),
		StepID: string(stepID),
	}, "error", cause)...)
}

// cancelIncompleteTools asks the Tool Broker to finish still-running tool_calls for runID.
func (s *Service) cancelIncompleteTools(ctx context.Context, runID kernel.RunID) {
	if s == nil || s.toolCalls == nil || runID == "" {
		return
	}
	if _, err := s.toolCalls.CancelIncompleteToolCalls(ctx, string(runID)); err != nil {
		slog.Warn("cancel incomplete tool calls failed", runlog.Attrs("chatsession", "cancel_tools", "failed", runlog.IDs{
			RunID: string(runID),
		}, "error", err)...)
	}
}

func (s *Service) publishTerminal(task kernel.Task, runID kernel.RunID) {
	if s == nil || s.stream == nil {
		return
	}
	s.stream.PublishTerminal(string(task.SessionID), string(task.ID), string(runID))
}

// resolveRunModelPin applies H7 job pin (strict, pre-validated at StartChat) then O4 session prefer.
// Nil → use daemon main.
func (s *Service) resolveRunModelPin(ctx context.Context, sessionID kernel.SessionID, jobModelRef string) *ModelPin {
	if s == nil || s.modelResolver == nil {
		return nil
	}
	if pin := strings.TrimSpace(jobModelRef); pin != "" {
		ep, err := s.modelResolver.ResolveStrict(pin)
		if err != nil {
			// StartChat already validated; treat as hard failure path via empty + log.
			slog.Error("job model pin resolve failed at execute", runlog.Attrs("chatsession", "model_pin", "failed", runlog.IDs{
				SessionID: string(sessionID),
			}, "pin", pin, "error", err)...)
			return nil
		}
		if ep != nil {
			return ep
		}
	}
	return s.sessionModelPin(ctx, sessionID)
}

// sessionModelPin loads session PreferredModel and resolves it (O4). Nil → use main.
func (s *Service) sessionModelPin(ctx context.Context, sessionID kernel.SessionID) *ModelPin {
	if s == nil || s.modelResolver == nil || s.repository == nil || sessionID == "" {
		return nil
	}
	session, err := s.repository.GetSession(ctx, sessionID)
	if err != nil {
		slog.Warn("session model pin load failed", runlog.Attrs("chatsession", "model_pin", "warning", runlog.IDs{
			SessionID: string(sessionID),
		}, "error", err)...)
		return nil
	}
	pref := strings.TrimSpace(session.PreferredModel)
	if pref == "" {
		return nil
	}
	return s.modelResolver.ResolveOrFallback(pref)
}

func (s *Service) sessionWorkspace(ctx context.Context, task kernel.Task) string {
	if s == nil {
		return ""
	}
	if s.repository != nil && task.SessionID != "" {
		if sess, err := s.repository.GetSession(ctx, task.SessionID); err == nil {
			if ws := strings.TrimSpace(sess.Workspace); ws != "" {
				return ws
			}
		}
	}
	if s.chatCfg != nil {
		if ws := s.chatCfg.ResolveSessionWorkspace("", s.daemonCWD); ws != "" {
			return ws
		}
	}
	return strings.TrimSpace(s.daemonCWD)
}

func (s *Service) agentsSystemMessage(ctx context.Context, task kernel.Task) *providerapi.Message {
	if s == nil {
		return nil
	}
	text := providerconfig.OverlayAgents(s.configDir, s.sessionWorkspace(ctx, task))
	if text == "" {
		return nil
	}
	return &providerapi.Message{Role: providerapi.RoleSystem, Content: text}
}

func (s *Service) skillCatalogMessage() *providerapi.Message {
	if s == nil || s.skills == nil {
		return nil
	}
	skills := s.skills.Skills()
	if len(skills) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("Available skills (id — one line). Load a body with skills_list then skill_view before following a workflow. Archived skills are omitted.")
	n := 0
	for _, sk := range skills {
		id := strings.TrimSpace(sk.ID)
		if id == "" {
			continue
		}
		desc := strings.TrimSpace(sk.Description)
		if desc == "" {
			desc = strings.TrimSpace(sk.Name)
		}
		line := id
		if desc != "" {
			line += " — " + desc
		}
		if err := injectscan.Scan(line); err != nil {
			continue
		}
		if n >= 64 {
			b.WriteString("\n…")
			break
		}
		b.WriteString("\n")
		b.WriteString(line)
		n++
	}
	if n == 0 {
		return nil
	}
	return &providerapi.Message{Role: providerapi.RoleSystem, Content: b.String()}
}

// skillSystemMessage loads the immutable task skill snapshot and builds a dedicated
// system message. Empty selection yields nil. Never re-reads SKILL.md files.
func (s *Service) skillSystemMessage(ctx context.Context, taskID kernel.TaskID) *providerapi.Message {
	if s == nil || s.repository == nil || taskID == "" {
		return nil
	}
	snapshot, err := s.repository.GetTaskSkillSnapshot(ctx, taskID)
	if err != nil {
		slog.Warn("chat skill snapshot load failed", runlog.Attrs("chatsession", "skill_inject", "warning", runlog.IDs{
			TaskID: string(taskID),
		}, "error", err)...)
		return nil
	}
	instructions := strings.TrimSpace(snapshot.Instructions)
	if instructions == "" {
		return nil
	}
	if err := injectscan.Scan(instructions); err != nil {
		slog.Warn("chat skill inject rejected", runlog.Attrs("chatsession", "skill_inject", "warning", runlog.IDs{
			TaskID: string(taskID),
		}, "error", err)...)
		return nil
	}
	content := skillSystemPreamble + "\n\n" + instructions
	return &providerapi.Message{Role: providerapi.RoleSystem, Content: content}
}

func (s *Service) existingChatRun(ctx context.Context, taskID kernel.TaskID) (kernel.RunID, bool, error) {
	runID := chatRunID(taskID)
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT run_id FROM runs WHERE run_id = ?", runID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return kernel.RunID(id), true, nil
}

func (s *Service) setActive(taskID kernel.TaskID, sessionID kernel.SessionID, cancel context.CancelFunc) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.active == nil {
		s.active = make(map[kernel.TaskID]activeTurn)
	}
	if prev := s.active[taskID]; prev.cancel != nil {
		prev.cancel()
	}
	s.active[taskID] = activeTurn{session: sessionID, cancel: cancel}
}

func (s *Service) clearActive(taskID kernel.TaskID) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	delete(s.active, taskID)
}

func (s *Service) sessionHasActive(sessionID kernel.SessionID) bool {
	if s == nil || sessionID == "" {
		return false
	}
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	for _, turn := range s.active {
		if turn.session == sessionID {
			return true
		}
	}
	return false
}

func (s *Service) taskState(ctx context.Context, taskID kernel.TaskID) (kernel.TaskState, error) {
	var state string
	if err := s.db.QueryRowContext(ctx, "SELECT state FROM tasks WHERE task_id = ?", taskID).Scan(&state); err != nil {
		return "", fmt.Errorf("load task state: %w", err)
	}
	return kernel.TaskState(state), nil
}

func chatPlanID(taskID kernel.TaskID) kernel.PlanID {
	return kernel.PlanID(deterministicID("chat-plan", string(taskID)))
}

func chatRunID(taskID kernel.TaskID) kernel.RunID {
	return kernel.RunID(deterministicID("chat-run", string(taskID)))
}

func deterministicID(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			_, _ = h.Write([]byte{0})
		}
		_, _ = h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, kernel.ErrNotFound), errors.Is(err, corequery.ErrNotFound):
		return applicationerror.Wrap(applicationerror.CodeNotFound, false, err)
	case errors.Is(err, kernel.ErrInvalidAggregate), errors.Is(err, approval.ErrInvalidPlan), errors.Is(err, ErrInvalidRequest):
		return applicationerror.Wrap(applicationerror.CodeInvalidRequest, false, err)
	case errors.Is(err, kernel.ErrAlreadyExists), errors.Is(err, approval.ErrAlreadyExists), errors.Is(err, kernel.ErrVersionConflict):
		return applicationerror.Wrap(applicationerror.CodeConflict, false, err)
	case errors.Is(err, ErrUnavailable):
		return applicationerror.Wrap(applicationerror.CodeUnavailable, false, err)
	default:
		return err
	}
}
