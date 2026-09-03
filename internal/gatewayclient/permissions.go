package gatewayclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Permission is a pending or decided tool-call permission (ADR-043).
type Permission struct {
	ID                string   `json:"permission_id"`
	SessionID         string   `json:"session_id,omitempty"`
	TaskID            string   `json:"task_id"`
	RunID             string   `json:"run_id"`
	ToolCallID        string   `json:"tool_call_id"`
	ToolName          string   `json:"tool_name"`
	Capability        string   `json:"capability,omitempty"`
	Path              string   `json:"path,omitempty"`
	Command           string   `json:"command,omitempty"`
	CommandArgs       []string `json:"command_args,omitempty"`
	NetworkDomain     string   `json:"network_domain,omitempty"`
	Risk              string   `json:"risk,omitempty"`
	State             string   `json:"state"`
	GrantID           string   `json:"grant_id,omitempty"`
	Decision          string   `json:"decision,omitempty"`
	CreatedAt         string   `json:"created_at"`
	DecidedAt         string   `json:"decided_at,omitempty"`
	SuggestedDecision string   `json:"suggested_decision,omitempty"`
	SuggestedReason   string   `json:"suggested_reason,omitempty"`
	ExtraRoot         bool     `json:"extra_root,omitempty"`
}

type permissionListResponse struct {
	Permissions []Permission `json:"permissions"`
}

// ListPermissions returns pending tool permissions (optional session filter).
type UserQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type UserQuestionItem struct {
	ID          string               `json:"id"`
	Question    string               `json:"question"`
	Header      string               `json:"header,omitempty"`
	Options     []UserQuestionOption `json:"options,omitempty"`
	MultiSelect bool                 `json:"multi_select,omitempty"`
}

type UserQuestion struct {
	ID        string              `json:"question_id"`
	SessionID string              `json:"session_id,omitempty"`
	TaskID    string              `json:"task_id,omitempty"`
	RunID     string              `json:"run_id,omitempty"`
	Questions []UserQuestionItem  `json:"questions"`
	State     string              `json:"state"`
	Answers   map[string][]string `json:"answers,omitempty"`
	CreatedAt string              `json:"created_at"`
}

type questionListResponse struct {
	Questions []UserQuestion `json:"questions"`
}

func (c *Client) ListQuestions(ctx context.Context, sessionID string, limit int) ([]UserQuestion, error) {
	path := "/v1/questions"
	q := url.Values{}
	if s := strings.TrimSpace(sessionID); s != "" {
		q.Set("session_id", s)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var response questionListResponse
	if err := c.inner.DoJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, fmt.Errorf("list questions: %w", err)
	}
	return response.Questions, nil
}

func (c *Client) AnswerQuestion(ctx context.Context, id, actor string, answers map[string][]string) (UserQuestion, error) {
	var result UserQuestion
	body := map[string]any{"answers": answers, "actor": strings.TrimSpace(actor)}
	path := "/v1/questions/" + url.PathEscape(strings.TrimSpace(id)) + "/answer"
	if err := c.inner.DoJSON(ctx, http.MethodPost, path, body, &result); err != nil {
		return UserQuestion{}, fmt.Errorf("answer question: %w", err)
	}
	return result, nil
}

func (c *Client) DismissQuestion(ctx context.Context, id, actor string) (UserQuestion, error) {
	var result UserQuestion
	body := map[string]any{"actor": strings.TrimSpace(actor)}
	path := "/v1/questions/" + url.PathEscape(strings.TrimSpace(id)) + "/dismiss"
	if err := c.inner.DoJSON(ctx, http.MethodPost, path, body, &result); err != nil {
		return UserQuestion{}, fmt.Errorf("dismiss question: %w", err)
	}
	return result, nil
}

func (c *Client) ListPermissions(ctx context.Context, sessionID string, limit int) ([]Permission, error) {
	path := "/v1/permissions"
	q := url.Values{}
	if s := strings.TrimSpace(sessionID); s != "" {
		q.Set("session_id", s)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var response permissionListResponse
	if err := c.inner.DoJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, fmt.Errorf("list permissions: %w", err)
	}
	return response.Permissions, nil
}

// DecidePermission applies allow_once | allow_similar | allow_permanent | deny.
func (c *Client) DecidePermission(ctx context.Context, permissionID, decision string) (Permission, error) {
	return c.DecidePermissionConfirm(ctx, permissionID, decision, false)
}

// DecidePermissionConfirm is DecidePermission with permanent second confirmation.
func (c *Client) DecidePermissionConfirm(ctx context.Context, permissionID, decision string, confirm bool) (Permission, error) {
	var item Permission
	path := "/v1/permissions/" + url.PathEscape(strings.TrimSpace(permissionID)) + "/decide"
	body := map[string]any{"decision": decision, "actor": "tui", "confirm": confirm}
	if err := c.inner.DoJSON(ctx, http.MethodPost, path, body, &item); err != nil {
		return Permission{}, fmt.Errorf("decide permission: %w", err)
	}
	return item, nil
}
