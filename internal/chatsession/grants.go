package chatsession

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/applicationerror"
	"github.com/yyZe0122/yunmengze-agent/internal/approval"
	"github.com/yyZe0122/yunmengze-agent/internal/audit"
	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
)

func (s *Service) resolveGrantRoots(ctx context.Context, task kernel.Task) []string {
	workspace := ""
	if sess, err := s.repository.GetSession(ctx, task.SessionID); err == nil {
		workspace = strings.TrimSpace(sess.Workspace)
	}
	if s.chatCfg != nil {
		if workspace == "" {
			workspace = s.chatCfg.ResolveSessionWorkspace("", s.daemonCWD)
		}
		if workspace == "" && len(s.roots) > 0 {
			workspace = s.roots[0]
		}
		roots := s.chatCfg.GrantRootsForSession(workspace)
		if len(roots) > 0 {
			if s.pathGuard != nil && workspace != "" {
				_ = s.pathGuard.AddRoot(workspace)
			}
			return roots
		}
	}
	if workspace != "" {
		if s.pathGuard != nil {
			_ = s.pathGuard.AddRoot(workspace)
		}
		out := []string{workspace}
		for _, r := range s.roots {
			if r != workspace {
				out = append(out, r)
			}
		}
		return out
	}
	return append([]string(nil), s.roots...)
}

type grantPosture struct {
	pregrantGit  bool
	pregrantProc bool
	askGit       bool
	askProc      bool
	cron         bool
}

func (s *Service) grantPosture(ctx context.Context, task kernel.Task) grantPosture {
	cron := strings.HasPrefix(string(task.ID), "scheduled_")
	stance := kernel.PermissionStanceAgent
	if !cron {
		if sess, err := s.repository.GetSession(ctx, task.SessionID); err == nil {
			if normalized, err := kernel.NormalizePermissionStance(sess.PermissionStance); err == nil {
				stance = normalized
			}
		}
	}
	agent := kernel.NormalizeExecutionMode(string(task.ExecutionMode)) == kernel.ExecutionModeAgent
	auto := agent && !cron && stance == kernel.PermissionStanceAuto
	preGit := agent && !cron && (s.allowGit || auto)
	preProc := agent && !cron && (s.allowProcess || auto)
	return grantPosture{
		pregrantGit: preGit, pregrantProc: preProc,
		askGit: agent && !cron && !preGit, askProc: agent && !cron && !preProc,
		cron: cron,
	}
}

func (s *Service) ensureChatWorkspaceAuth(ctx context.Context, task kernel.Task) (approval.PlanDocument, string, kernel.Task, grantPosture, error) {
	posture := s.grantPosture(ctx, task)
	planID := chatPlanID(task.ID)
	var storedHash, state, document string
	err := s.db.QueryRowContext(ctx, `
		SELECT scope_hash, state, document FROM plans WHERE plan_id = ? AND task_id = ?`,
		planID, task.ID,
	).Scan(&storedHash, &state, &document)
	if err == nil {
		if state != string(kernel.PlanApproved) {
			return approval.PlanDocument{}, "", task, posture, applicationerror.Wrap(applicationerror.CodeConflict, false,
				fmt.Errorf("%w: chat plan %s is %s, want approved", ErrInvalidRequest, planID, state))
		}
		var plan approval.PlanDocument
		if err := json.Unmarshal([]byte(document), &plan); err != nil {
			return approval.PlanDocument{}, "", task, posture, fmt.Errorf("decode chat plan: %w", err)
		}
		if err := s.recordSystemApproval(ctx, plan); err != nil {
			return approval.PlanDocument{}, "", task, posture, err
		}
		task, err = s.repository.GetTask(ctx, task.ID)
		if err != nil {
			return approval.PlanDocument{}, "", task, posture, classify(err)
		}
		return plan, storedHash, task, posture, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return approval.PlanDocument{}, "", task, posture, fmt.Errorf("lookup chat plan: %w", err)
	}

	grantRoots := s.resolveGrantRoots(ctx, task)
	if len(grantRoots) == 0 {
		return approval.PlanDocument{}, "", task, posture, applicationerror.Wrap(applicationerror.CodeInvalidRequest, false,
			fmt.Errorf("%w: no workspace roots for session", ErrInvalidRequest))
	}
	plan := s.buildWorkspacePlan(planID, task.ID, kernel.NormalizeExecutionMode(string(task.ExecutionMode)), grantRoots, posture)
	documentBytes, err := plan.CanonicalJSON()
	if err != nil {
		return approval.PlanDocument{}, "", task, posture, err
	}
	hash, err := plan.Hash()
	if err != nil {
		return approval.PlanDocument{}, "", task, posture, err
	}
	drafts := []kernel.PlanStepDraft{{
		ID: plan.Steps[0].StepID, Position: 0, Title: plan.Steps[0].Title, EffectLevel: string(plan.Steps[0].Risk),
	}}
	_, task, err = s.repository.CreateApprovedWorkspacePlan(
		ctx, plan.PlanID, task.ID, task.Version, plan.Revision, hash, documentBytes, drafts,
		"session chat workspace auth", s.now(),
	)
	if err != nil {
		return approval.PlanDocument{}, "", task, posture, classify(err)
	}
	if err := s.recordSystemApproval(ctx, plan); err != nil {
		return approval.PlanDocument{}, "", task, posture, err
	}
	return plan, hash, task, posture, nil
}

func (s *Service) recordSystemApproval(ctx context.Context, plan approval.PlanDocument) error {
	approvalID := approval.ApprovalID(deterministicID("chat-approval", string(plan.PlanID)))
	expires := s.now().UTC().Add(24 * time.Hour)
	_, err := s.approvals.RecordSystemApproval(ctx, approval.DecisionInput{
		ID: approvalID, Plan: plan, Scope: approval.ScopePlan,
		Decision: approval.DecisionApproved, DecidedBy: "session-workspace-auth",
		Reason: "session chat workspace preauthorization", DecidedAt: s.now(), ExpiresAt: &expires,
	})
	if err != nil && !errors.Is(err, approval.ErrAlreadyExists) {
		return classify(err)
	}
	return nil
}

func (s *Service) createChatRun(ctx context.Context, task kernel.Task, plan approval.PlanDocument, planHash, actor, traceID string) (kernel.RunID, error) {
	runID := chatRunID(task.ID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin chat run: %w", err)
	}
	defer tx.Rollback()

	var existing string
	err = tx.QueryRowContext(ctx, "SELECT run_id FROM runs WHERE run_id = ?", runID).Scan(&existing)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return runID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("lookup chat run: %w", err)
	}

	now := s.now().UTC()
	// Workspace auth already set task to running; only accept running (or created for races).
	result, err := tx.ExecContext(ctx, `
		UPDATE tasks SET state = ?, version = version + 1, updated_at = ?
		WHERE task_id = ? AND state IN (?, ?)`,
		kernel.TaskRunning, formatTime(now), task.ID,
		kernel.TaskRunning, kernel.TaskCreated,
	)
	if err != nil {
		return "", fmt.Errorf("start chat task: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		var state string
		_ = tx.QueryRowContext(ctx, "SELECT state FROM tasks WHERE task_id = ?", task.ID).Scan(&state)
		if state != string(kernel.TaskRunning) && state != string(kernel.TaskCompleted) && state != string(kernel.TaskFailed) {
			return "", applicationerror.Wrap(applicationerror.CodeConflict, false, fmt.Errorf("%w: cannot start chat task in state %s", ErrInvalidRequest, state))
		}
	}
	_, _ = tx.ExecContext(ctx, `
		UPDATE plan_steps SET state = ?, version = version + 1, updated_at = ?
		WHERE plan_id = ? AND state IN (?, ?)`,
		kernel.StepApproved, formatTime(now), plan.PlanID, kernel.StepPending, kernel.StepApproved,
	)
	stepID := planChatStepID(plan)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO runs (run_id, task_id, plan_id, state, started_at, finished_at, error, version, updated_at, step_id)
		VALUES (?, ?, ?, ?, ?, NULL, NULL, 1, ?, ?)`,
		runID, task.ID, plan.PlanID, kernel.RunCreated, formatTime(now), formatTime(now), stepID,
	)
	if err != nil {
		return "", fmt.Errorf("insert chat run: %w", err)
	}
	if err := s.audit.RecordTx(ctx, tx, audit.Entry{
		OccurredAt: now, Actor: actor, Action: "chat.start", ResourceType: "run", ResourceID: string(runID),
		Outcome: "accepted", TraceID: traceID,
		Details: map[string]any{"task_id": task.ID, "session_id": task.SessionID, "plan_id": plan.PlanID, "plan_hash": planHash},
	}); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit chat run: %w", err)
	}
	return runID, nil
}

func (s *Service) issueChatGrants(ctx context.Context, plan approval.PlanDocument, posture grantPosture) (map[string][]string, error) {
	step := plan.Steps[0]
	hash, err := plan.Hash()
	if err != nil {
		return nil, err
	}
	var approvalID string
	err = s.db.QueryRowContext(ctx, `
		SELECT approval_id FROM approvals
		WHERE plan_id = ? AND plan_revision = ? AND scope_hash = ? AND decision = ?
		AND scope_type = ? AND step_id IS NULL AND invalidated_at IS NULL
		ORDER BY decided_at DESC, approval_id DESC LIMIT 1`,
		plan.PlanID, plan.Revision, hash, approval.DecisionApproved, approval.ScopePlan,
	).Scan(&approvalID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, approval.ErrNotApproved
	}
	if err != nil {
		return nil, fmt.Errorf("load chat approval: %w", err)
	}
	issuedAt := s.now().UTC()
	expiresAt := issuedAt.Add(time.Duration(plan.Budget.MaxDurationMillis) * time.Millisecond)
	if min := issuedAt.Add(time.Hour); expiresAt.Before(min) {
		expiresAt = min
	}
	grants := make(map[string][]string)
	for _, scope := range step.Capabilities {
		if !posture.shouldIssue(scope) {
			continue
		}
		grantID := approval.GrantID(deterministicID("chat-grant", approvalID, string(step.StepID), scope.Capability, fmt.Sprintf("%v", scope.OneTime), fmt.Sprintf("%d", scope.MaxCalls)))
		_, err = s.approvals.IssueGrant(ctx, approval.GrantInput{
			ID: grantID, ApprovalID: approval.ApprovalID(approvalID), Plan: plan, StepID: step.StepID,
			Scope: scope, IssuedAt: issuedAt, ExpiresAt: expiresAt,
		})
		if err != nil && !errors.Is(err, approval.ErrAlreadyExists) {
			return nil, err
		}
		grants[scope.Capability] = append(grants[scope.Capability], string(grantID))
	}
	return grants, nil
}

func uniqueCapabilityNames(scopes []approval.CapabilityScope) []string {
	seen := make(map[string]struct{}, len(scopes))
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		name := strings.TrimSpace(scope.Capability)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func (p grantPosture) shouldIssue(scope approval.CapabilityScope) bool {
	name := scope.Capability
	if name == "process_exec" || name == "process_shell" {
		return p.pregrantProc && !scope.OneTime
	}
	if strings.HasPrefix(name, "git_") {
		return p.pregrantGit && !scope.OneTime
	}
	if name == "http_get" || name == "web_search" || name == "web_extract" {
		return false
	}
	if name == "vision_analyze" && len(scope.Paths) == 0 {
		return false
	}
	return !scope.OneTime
}

func (s *Service) buildWorkspacePlan(planID kernel.PlanID, taskID kernel.TaskID, mode kernel.ExecutionMode, roots []string, posture grantPosture) approval.PlanDocument {
	if len(roots) == 0 {
		roots = append([]string(nil), s.roots...)
	}
	pathRoots := append([]string(nil), roots...)
	caps := []approval.CapabilityScope{
		{Capability: "fs_read", Paths: pathRoots, MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls},
		{Capability: "fs_list", Paths: append([]string(nil), pathRoots...), MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls},
		{Capability: "fs_stat", Paths: append([]string(nil), pathRoots...), MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls},
		{Capability: "fs_glob", Paths: append([]string(nil), pathRoots...), MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls},
		{Capability: "fs_grep", Paths: append([]string(nil), pathRoots...), MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls},
	}
	risk := policy.RiskR0
	effects := []string{"read workspace files when needed"}
	// Agent (build) gets write tools unless writeCeiling is false. Plan is always read-only.
	allowWrite := mode == kernel.ExecutionModeAgent && s.writeCeiling
	if allowWrite {
		caps = append(caps,
			approval.CapabilityScope{Capability: "fs_write", Paths: append([]string(nil), pathRoots...), MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls},
			approval.CapabilityScope{Capability: "fs_patch", Paths: append([]string(nil), pathRoots...), MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls},
			approval.CapabilityScope{Capability: "fs_mkdir", Paths: append([]string(nil), pathRoots...), MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls},
			approval.CapabilityScope{Capability: "fs_remove", Paths: append([]string(nil), pathRoots...), MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls},
		)
		risk = policy.RiskR1
		effects = append(effects, "write workspace files when needed")
	}
	mcpNames := append([]string(nil), s.extraTools...)
	slices.Sort(mcpNames)
	for _, name := range mcpNames {
		caps = append(caps, approval.CapabilityScope{
			Capability: name, MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls,
		})
	}
	if len(mcpNames) > 0 {
		effects = append(effects, "use configured MCP tools")
		risk = policy.RiskR2
	}
	caps = append(caps,
		approval.CapabilityScope{Capability: "skills_list", MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls},
		approval.CapabilityScope{Capability: "skill_view", MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls},
		approval.CapabilityScope{Capability: "ask_user", MaxDurationMillis: 15 * 60 * 1000, MaxCalls: defaultMaxCalls},
	)
	effects = append(effects, "list and load local skill instructions", "ask the user multiple-choice questions")
	if posture.pregrantGit || posture.askGit {
		for _, name := range []string{"git_status", "git_diff", "git_add", "git_commit"} {
			if posture.pregrantGit {
				caps = append(caps, approval.CapabilityScope{
					Capability: name, Paths: append([]string(nil), pathRoots...),
					MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls,
				})
			} else {
				caps = append(caps,
					approval.CapabilityScope{
						Capability: name, Paths: append([]string(nil), pathRoots...),
						MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: 1, OneTime: true,
					},
					approval.CapabilityScope{
						Capability: name, Paths: append([]string(nil), pathRoots...),
						MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls, OneTime: false,
					},
				)
			}
		}
		risk = policy.RiskR2
		effects = append(effects, "use git tools under workspace roots")
	}
	if kernel.NormalizeExecutionMode(string(mode)) == kernel.ExecutionModeAgent && !posture.cron {
		for _, name := range []string{"http_get", "web_search", "web_extract"} {
			caps = append(caps,
				approval.CapabilityScope{
					Capability: name, MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: 1, OneTime: true,
				},
				approval.CapabilityScope{
					Capability: name, MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls, OneTime: false,
				},
			)
		}
		effects = append(effects, "search the web and fetch approved http/https URLs after /perm (SSRF baseline still applies)")
		risk = policy.RiskR2
	}
	if hasConfiguredRole(s.configuredRoles, providerconfig.RoleVision) {
		caps = append(caps, approval.CapabilityScope{
			Capability: "vision_analyze", Paths: append([]string(nil), pathRoots...),
			MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls,
		})
		caps = append(caps, approval.CapabilityScope{
			Capability: "video_analyze", Paths: append([]string(nil), pathRoots...),
			MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls,
		})
		if kernel.NormalizeExecutionMode(string(mode)) == kernel.ExecutionModeAgent && !posture.cron {
			caps = append(caps,
				approval.CapabilityScope{
					Capability: "vision_analyze", MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: 1, OneTime: true,
				},
				approval.CapabilityScope{
					Capability: "vision_analyze", MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls, OneTime: false,
				},
			)
		}
		effects = append(effects, "analyze workspace images and video frames with models.vision")
		if risk == policy.RiskR0 {
			risk = policy.RiskR1
		}
	}
	if hasConfiguredRole(s.configuredRoles, providerconfig.RoleSpeech) {
		caps = append(caps, approval.CapabilityScope{
			Capability: "audio_transcribe", Paths: append([]string(nil), pathRoots...),
			MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls,
		})
		effects = append(effects, "transcribe workspace audio with models.speech")
		if risk == policy.RiskR0 {
			risk = policy.RiskR1
		}
	}
	// Logical sub-agent (ADR-039): both modes may spawn task; grants do not expand FS.
	caps = append(caps, approval.CapabilityScope{
		Capability: "task", MaxDurationMillis: defaultMaxDurationMS, MaxCalls: defaultMaxCalls,
	})
	effects = append(effects, "delegate synchronous sub-agent tasks")
	// In-process memory tools (ADR-044): search is R0; write/promote need plan scope + grant (agent only).
	caps = append(caps,
		approval.CapabilityScope{
			Capability: "memory_search", MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls,
		},
		approval.CapabilityScope{
			Capability: "session_search", MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls,
		},
		approval.CapabilityScope{
			Capability: "todo_list", MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls,
		},
		approval.CapabilityScope{
			Capability: "todo_write", MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls,
		},
	)
	if mode == kernel.ExecutionModeAgent {
		caps = append(caps,
			approval.CapabilityScope{
				Capability: "memory_write", MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls,
			},
			approval.CapabilityScope{
				Capability: "memory_promote", MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls,
			},
			approval.CapabilityScope{
				Capability: "skill_draft", MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls,
			},
		)
		effects = append(effects, "search/write local memory and past transcripts")
		effects = append(effects, "propose skill drafts (user must apply)")
		if risk == policy.RiskR0 {
			risk = policy.RiskR1
		}
	} else {
		effects = append(effects, "search local memory and past transcripts")
	}
	if posture.pregrantProc || posture.askProc {
		for _, name := range []string{"process_exec", "process_shell"} {
			if posture.pregrantProc {
				caps = append(caps, approval.CapabilityScope{
					Capability: name, Paths: append([]string(nil), pathRoots...),
					MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls,
				})
			} else {
				caps = append(caps,
					approval.CapabilityScope{
						Capability: name, Paths: append([]string(nil), pathRoots...),
						MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: 1, OneTime: true,
					},
					approval.CapabilityScope{
						Capability: name, Paths: append([]string(nil), pathRoots...),
						MaxDurationMillis: defaultToolTimeoutMS, MaxCalls: defaultMaxCalls, OneTime: false,
					},
				)
			}
		}
		risk = policy.RiskR2
		effects = append(effects, "execute processes under workspace roots")
	}
	if risk == policy.RiskR0 {
		risk = policy.RiskR1
	}
	objective := "session chat workspace tools"
	title := "session chat"
	if mode == kernel.ExecutionModePlan {
		objective = "session plan-mode read-only workspace tools"
		title = "session plan (read-only)"
	}
	return approval.PlanDocument{
		PlanID: planID, TaskID: taskID, Revision: 1,
		Objective: objective,
		Budget: approval.PlanBudget{
			MaxTokens: defaultMaxTokens, MaxCostMicros: 0, MaxDurationMillis: defaultMaxDurationMS,
		},
		Steps: []approval.StepScope{{
			StepID: chatStepID(taskID), Position: 0, Title: title,
			Risk: risk, ExpectedSideEffects: effects,
			Rollback: "none", TimeoutMillis: defaultToolTimeoutMS, Capabilities: caps,
		}},
	}
}

func hasConfiguredRole(roles []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, role := range roles {
		if strings.TrimSpace(role) == want {
			return true
		}
	}
	return false
}
