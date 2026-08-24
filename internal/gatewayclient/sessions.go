package gatewayclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// CompactResult is the response from POST /v1/sessions/{id}/compact.
type CompactResult struct {
	SessionID    string `json:"session_id"`
	Summary      string `json:"summary"`
	Source       string `json:"source"`
	CompactionID string `json:"compaction_id,omitempty"`
}

type sessionListResponse struct {
	Sessions []Session `json:"sessions"`
}

type messageListResponse struct {
	Messages []TranscriptMessage `json:"messages"`
}

func (c *Client) ListSessions(ctx context.Context, limit int) ([]Session, error) {
	path := "/v1/sessions"
	if limit > 0 {
		path += "?limit=" + fmt.Sprintf("%d", limit)
	}
	var response sessionListResponse
	if err := c.inner.DoJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return response.Sessions, nil
}

func (c *Client) GetSession(ctx context.Context, id SessionID) (Session, error) {
	var session Session
	if err := c.inner.DoJSON(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(string(id)), nil, &session); err != nil {
		return Session{}, fmt.Errorf("get session: %w", err)
	}
	return session, nil
}

// SetSessionPreferredModel stores a session model preference (O4; chat run job pin → prefer → main; no global switch).
// Empty model clears the preference.
func (c *Client) SetSessionPreferredModel(ctx context.Context, id SessionID, model string) (Session, error) {
	var session Session
	body := map[string]string{"preferred_model": strings.TrimSpace(model)}
	if err := c.inner.DoJSON(ctx, http.MethodPatch, "/v1/sessions/"+url.PathEscape(string(id)), body, &session); err != nil {
		return Session{}, fmt.Errorf("set session preferred model: %w", err)
	}
	return session, nil
}

func (c *Client) SetSessionPermissionStance(ctx context.Context, id SessionID, stance string) (Session, error) {
	var session Session
	body := map[string]string{"permission_stance": strings.TrimSpace(stance)}
	if err := c.inner.DoJSON(ctx, http.MethodPatch, "/v1/sessions/"+url.PathEscape(string(id)), body, &session); err != nil {
		return Session{}, fmt.Errorf("set session permission stance: %w", err)
	}
	return session, nil
}

type SessionTodo struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Status    string `json:"status"`
	Position  int    `json:"position"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type sessionTodoListResponse struct {
	Todos []SessionTodo `json:"todos"`
}

func (c *Client) ListSessionTodos(ctx context.Context, id SessionID) ([]SessionTodo, error) {
	path := "/v1/sessions/" + url.PathEscape(string(id)) + "/todos"
	var response sessionTodoListResponse
	if err := c.inner.DoJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, fmt.Errorf("session todos: %w", err)
	}
	return response.Todos, nil
}

func (c *Client) SessionMessages(ctx context.Context, id SessionID, limit int) ([]TranscriptMessage, error) {
	path := "/v1/sessions/" + url.PathEscape(string(id)) + "/messages"
	if limit > 0 {
		path += "?limit=" + fmt.Sprintf("%d", limit)
	}
	var response messageListResponse
	if err := c.inner.DoJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, fmt.Errorf("session messages: %w", err)
	}
	return response.Messages, nil
}

func (c *Client) TaskMessages(ctx context.Context, id TaskID, limit int) ([]TranscriptMessage, error) {
	path := "/v1/tasks/" + url.PathEscape(string(id)) + "/messages"
	if limit > 0 {
		path += "?limit=" + fmt.Sprintf("%d", limit)
	}
	var response messageListResponse
	if err := c.inner.DoJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, fmt.Errorf("task messages: %w", err)
	}
	return response.Messages, nil
}

// CompactSession forces durable head summarization for the session (manual /compact).
func (c *Client) CompactSession(ctx context.Context, id SessionID, focus string) (CompactResult, error) {
	var result CompactResult
	body := map[string]string{}
	if f := strings.TrimSpace(focus); f != "" {
		body["focus"] = f
	}
	path := "/v1/sessions/" + url.PathEscape(string(id)) + "/compact"
	if err := c.inner.DoJSON(ctx, http.MethodPost, path, body, &result); err != nil {
		return CompactResult{}, fmt.Errorf("compact session: %w", err)
	}
	return result, nil
}

// RewindResult is the response from POST /v1/sessions/{id}/rewind.
type RewindResult struct {
	SessionID  string `json:"session_id"`
	RevisionID string `json:"revision_id"`
	Path       string `json:"path"`
}

// SteerResult is the response from POST /v1/sessions/{id}/steer.
type SteerResult struct {
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id"`
	RunID     string `json:"run_id"`
	ItemID    string `json:"item_id"`
}

func (c *Client) SteerSession(ctx context.Context, id SessionID, text string) (SteerResult, error) {
	var result SteerResult
	body := map[string]string{"text": strings.TrimSpace(text)}
	path := "/v1/sessions/" + url.PathEscape(string(id)) + "/steer"
	if err := c.inner.DoJSON(ctx, http.MethodPost, path, body, &result); err != nil {
		return SteerResult{}, fmt.Errorf("steer session: %w", err)
	}
	return result, nil
}

func (c *Client) RewindSession(ctx context.Context, id SessionID, revisionID string) (RewindResult, error) {
	var result RewindResult
	body := map[string]string{}
	if r := strings.TrimSpace(revisionID); r != "" {
		body["revision_id"] = r
	}
	path := "/v1/sessions/" + url.PathEscape(string(id)) + "/rewind"
	if err := c.inner.DoJSON(ctx, http.MethodPost, path, body, &result); err != nil {
		return RewindResult{}, fmt.Errorf("rewind session: %w", err)
	}
	return result, nil
}
