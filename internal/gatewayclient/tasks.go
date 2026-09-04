package gatewayclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/approval"
	"github.com/yyZe0122/yunmengze-agent/internal/taskcontrol"
)

type TaskSubmissionRequest struct {
	TaskID        TaskID    `json:"task_id,omitempty"`
	SessionID     SessionID `json:"session_id,omitempty"`
	PlanID        PlanID    `json:"plan_id,omitempty"`
	Title         string    `json:"title"`
	Objective     string    `json:"objective"`
	SkillIDs      []string  `json:"skill_ids,omitempty"`
	ExecutionMode string    `json:"execution_mode,omitempty"`
	// Workspace is the client launch directory (absolute); bound to session (ADR-046).
	Workspace string `json:"workspace,omitempty"`
	// PermissionStance is the Tab posture (agent|auto|plan). Not execution_mode.
	PermissionStance string `json:"permission_stance,omitempty"`
	// PreferredModel is an optional session model preference written before chat start (O4).
	PreferredModel string `json:"preferred_model,omitempty"`
	// Interactive is true when the client can decide /perm (TUI).
	Interactive bool `json:"interactive,omitempty"`
}

type TaskSubmissionResponse struct {
	Task  Task                   `json:"task"`
	Plan  *approval.PlanDocument `json:"plan,omitempty"`
	RunID RunID                  `json:"run_id,omitempty"`
	// PlanID from client (plan mode) or daemon chat synthetic plan (agent mode).
	PlanID PlanID `json:"plan_id,omitempty"`
}

type taskListResponse struct {
	Tasks []Task `json:"tasks"`
}

func (c *Client) SubmitTask(ctx context.Context, request TaskSubmissionRequest) (TaskSubmissionResponse, error) {
	if strings.TrimSpace(request.Title) == "" {
		request.Title = TaskTitle(request.Objective)
	}
	mode := strings.TrimSpace(request.ExecutionMode)
	if mode == "" {
		mode = ExecutionModeAgent
	}
	request.ExecutionMode = mode
	// Both modes use daemon-owned synthetic chat plans; ignore client plan IDs.
	request.PlanID = ""
	request.Workspace = strings.TrimSpace(request.Workspace)
	var response TaskSubmissionResponse
	if err := c.inner.DoJSON(ctx, http.MethodPost, "/v1/tasks", request, &response); err != nil {
		return TaskSubmissionResponse{}, fmt.Errorf("create task: %w", err)
	}
	if response.PlanID == "" {
		response.PlanID = request.PlanID
	}
	return response, nil
}

func (c *Client) GetTask(ctx context.Context, taskID TaskID) (Task, error) {
	var task Task
	if err := c.inner.DoJSON(ctx, http.MethodGet, "/v1/tasks/"+url.PathEscape(string(taskID)), nil, &task); err != nil {
		return Task{}, fmt.Errorf("get task: %w", err)
	}
	return task, nil
}

func (c *Client) TaskUsage(ctx context.Context, taskID TaskID) (TaskUsage, error) {
	var usage TaskUsage
	path := "/v1/tasks/" + url.PathEscape(string(taskID)) + "/usage"
	if err := c.inner.DoJSON(ctx, http.MethodGet, path, nil, &usage); err != nil {
		return TaskUsage{}, fmt.Errorf("task usage: %w", err)
	}
	return usage, nil
}

// TaskContext returns window-fill pressure for a task (not lifetime token spend).
func (c *Client) TaskContext(ctx context.Context, taskID TaskID) (TaskContext, error) {
	var item TaskContext
	path := "/v1/tasks/" + url.PathEscape(string(taskID)) + "/context"
	if err := c.inner.DoJSON(ctx, http.MethodGet, path, nil, &item); err != nil {
		return TaskContext{}, fmt.Errorf("task context: %w", err)
	}
	return item, nil
}

// SessionContext returns the latest context snapshot for a session.
func (c *Client) SessionContext(ctx context.Context, sessionID SessionID) (TaskContext, error) {
	var item TaskContext
	path := "/v1/sessions/" + url.PathEscape(string(sessionID)) + "/context"
	if err := c.inner.DoJSON(ctx, http.MethodGet, path, nil, &item); err != nil {
		return TaskContext{}, fmt.Errorf("session context: %w", err)
	}
	return item, nil
}

func (c *Client) ListTasks(ctx context.Context, limit int) ([]Task, error) {
	path := "/v1/tasks"
	if limit > 0 {
		path += "?limit=" + fmt.Sprintf("%d", limit)
	}
	var response taskListResponse
	if err := c.inner.DoJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return response.Tasks, nil
}

func (c *Client) ControlTask(ctx context.Context, taskID TaskID, action TaskAction, expectedVersion uint64, reason string) (Task, error) {
	var updated Task
	if err := c.inner.DoJSON(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(string(taskID))+"/actions", taskcontrol.TaskActionRequest{
		ExpectedVersion: expectedVersion,
		Action:          action,
		Reason:          strings.TrimSpace(reason),
	}, &updated); err != nil {
		return Task{}, fmt.Errorf("%s task: %w", action, err)
	}
	return updated, nil
}

func (c *Client) GetPlan(ctx context.Context, planID PlanID) (Plan, error) {
	var plan Plan
	if err := c.inner.DoJSON(ctx, http.MethodGet, "/v1/plans/"+url.PathEscape(string(planID)), nil, &plan); err != nil {
		return Plan{}, fmt.Errorf("get plan: %w", err)
	}
	return plan, nil
}

type planListResponse struct {
	Plans []Plan `json:"plans"`
}

func (c *Client) ListPlans(ctx context.Context, limit int) ([]Plan, error) {
	path := "/v1/plans"
	if limit > 0 {
		path += "?limit=" + fmt.Sprintf("%d", limit)
	}
	var response planListResponse
	if err := c.inner.DoJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	return response.Plans, nil
}

func (c *Client) FindPlanForTask(ctx context.Context, taskID TaskID) (Plan, error) {
	plans, err := c.ListPlans(ctx, 100)
	if err != nil {
		return Plan{}, err
	}
	for _, plan := range plans {
		if string(plan.TaskID) == string(taskID) {
			return plan, nil
		}
	}
	return Plan{}, fmt.Errorf("no plan found for task %s", taskID)
}

func TaskTitle(objective string) string {
	runes := []rune(strings.TrimSpace(objective))
	if len(runes) <= 80 {
		return string(runes)
	}
	return string(runes[:80]) + "…"
}

func RandomID(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate ID: %w", err)
	}
	return prefix + hex.EncodeToString(value), nil
}
