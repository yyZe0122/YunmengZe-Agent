package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/applicationerror"
	"github.com/yyZe0122/yunmengze-agent/internal/corequery"
	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
)

type sessionTodosStub struct {
	items []corequery.SessionTodo
	err   error
}

func (s *sessionTodosStub) Check(context.Context) error { return nil }
func (s *sessionTodosStub) ListSessions(context.Context, corequery.SessionListOptions) ([]corequery.Session, error) {
	return nil, nil
}
func (s *sessionTodosStub) GetSession(context.Context, kernel.SessionID) (corequery.Session, error) {
	return corequery.Session{}, nil
}
func (s *sessionTodosStub) SessionTranscript(context.Context, kernel.SessionID, corequery.TranscriptOptions) ([]corequery.TranscriptMessage, error) {
	return nil, nil
}
func (s *sessionTodosStub) ListSessionTodos(_ context.Context, id kernel.SessionID) ([]corequery.SessionTodo, error) {
	if s.err != nil {
		return nil, s.err
	}
	if id == "" {
		return nil, corequery.ErrNotFound
	}
	return s.items, nil
}
func (s *sessionTodosStub) TaskTranscript(context.Context, kernel.TaskID, corequery.TranscriptOptions) ([]corequery.TranscriptMessage, error) {
	return nil, nil
}
func (s *sessionTodosStub) ListTasks(context.Context, corequery.TaskListOptions) ([]corequery.Task, error) {
	return nil, nil
}
func (s *sessionTodosStub) GetTask(context.Context, kernel.TaskID) (corequery.Task, error) {
	return corequery.Task{}, nil
}
func (s *sessionTodosStub) TaskUsage(context.Context, kernel.TaskID) (corequery.TaskUsage, error) {
	return corequery.TaskUsage{}, nil
}
func (s *sessionTodosStub) RunUsage(context.Context, kernel.RunID) (corequery.RunUsage, error) {
	return corequery.RunUsage{}, nil
}
func (s *sessionTodosStub) TaskContext(context.Context, kernel.TaskID) (corequery.TaskContext, error) {
	return corequery.TaskContext{}, nil
}
func (s *sessionTodosStub) SessionContext(context.Context, kernel.SessionID) (corequery.TaskContext, error) {
	return corequery.TaskContext{}, nil
}
func (s *sessionTodosStub) ListPlans(context.Context, corequery.PlanListOptions) ([]corequery.Plan, error) {
	return nil, nil
}
func (s *sessionTodosStub) GetPlan(context.Context, kernel.PlanID) (corequery.Plan, error) {
	return corequery.Plan{}, nil
}
func (s *sessionTodosStub) ListApprovals(context.Context, corequery.ApprovalListOptions) ([]corequery.Approval, error) {
	return nil, nil
}
func (s *sessionTodosStub) ListRuns(context.Context, corequery.RunListOptions) ([]corequery.Run, error) {
	return nil, nil
}
func (s *sessionTodosStub) GetRun(context.Context, kernel.RunID) (corequery.Run, error) {
	return corequery.Run{}, nil
}
func (s *sessionTodosStub) ListMemory(context.Context, corequery.MemoryListOptions) ([]corequery.MemoryEntry, error) {
	return nil, nil
}
func (s *sessionTodosStub) ListSkillEvents(context.Context, corequery.SkillEventListOptions) ([]corequery.SkillEvent, error) {
	return nil, nil
}

func TestSessionTodosEndpoint(t *testing.T) {
	api := &API{queries: &sessionTodosStub{items: []corequery.SessionTodo{
		{ID: "t1", Content: "patch fs.go", Status: "in_progress", Position: 0},
	}}}
	request := httptest.NewRequest(http.MethodGet, "/v1/sessions/session-1/todos", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Todos []corequery.SessionTodo `json:"todos"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Todos) != 1 || body.Todos[0].ID != "t1" || body.Todos[0].Status != "in_progress" {
		t.Fatalf("todos = %+v", body.Todos)
	}
}

func TestSessionTodosEndpointNotFound(t *testing.T) {
	api := &API{queries: &sessionTodosStub{err: corequery.ErrNotFound}}
	request := httptest.NewRequest(http.MethodGet, "/v1/sessions/missing/todos", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestSessionTodosEndpointRejectsNestedPath(t *testing.T) {
	api := &API{queries: &sessionTodosStub{}}
	request := httptest.NewRequest(http.MethodGet, "/v1/sessions/a/b/todos", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

type sessionRetractStub struct {
	result SessionRetractResult
	err    error
}

func (s *sessionRetractStub) RetractLastTurn(context.Context, kernel.SessionID, bool) (SessionRetractResult, error) {
	return s.result, s.err
}

func TestSessionRetractConflictUsesErrorEnvelope(t *testing.T) {
	api := &API{sessionRetract: &sessionRetractStub{
		result: SessionRetractResult{SessionID: "s1", TaskID: "t1", FailedPath: "/tmp/a.go", FailedReason: "file changed"},
		err:    applicationerror.Wrap(applicationerror.CodeConflict, false, errors.New("file changed since revision")),
	}}
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/s1/retract", strings.NewReader(`{"rewind_files":true}`))
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"error"`) {
		t.Fatalf("expected error envelope: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "user_text") {
		t.Fatalf("should not return retract result on conflict: %s", response.Body.String())
	}
}

func TestSessionRetractOK(t *testing.T) {
	api := &API{sessionRetract: &sessionRetractStub{
		result: SessionRetractResult{SessionID: "s1", TaskID: "t1", UserText: "rewrite me"},
	}}
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/s1/retract", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var body SessionRetractResult
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.UserText != "rewrite me" || body.TaskID != "t1" {
		t.Fatalf("body = %+v", body)
	}
}

func TestSessionTodosEndpointRejectsPost(t *testing.T) {
	api := &API{queries: &sessionTodosStub{}}
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/session-1/todos", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}
