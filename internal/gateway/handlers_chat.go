package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/approval"
	"github.com/yyZe0122/yunmengze-agent/internal/corequery"
	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	"github.com/yyZe0122/yunmengze-agent/internal/runlog"
	"github.com/yyZe0122/yunmengze-agent/internal/taskcontrol"
	"github.com/yyZe0122/yunmengze-agent/internal/tasksubmission"
)

type taskSubmissionRequest struct {
	TaskID        kernel.TaskID        `json:"task_id"`
	SessionID     kernel.SessionID     `json:"session_id"`
	PlanID        kernel.PlanID        `json:"plan_id"`
	Title         string               `json:"title"`
	Objective     string               `json:"objective"`
	SkillIDs      []string             `json:"skill_ids,omitempty"`
	ExecutionMode kernel.ExecutionMode `json:"execution_mode,omitempty"`
	// Workspace is the client launch directory (absolute); bound to session (ADR-046).
	Workspace string `json:"workspace,omitempty"`
	// PermissionStance is the Tab posture written onto the session (agent|auto|plan).
	PermissionStance string `json:"permission_stance,omitempty"`
	// PreferredModel is an optional session model preference written before chat start (O4).
	PreferredModel string `json:"preferred_model,omitempty"`
	// Interactive is true for TUI turns that can answer /perm. CLI/cron omit it.
	// Local capability flag, not authentication.
	Interactive bool `json:"interactive,omitempty"`
}

type taskSubmissionResponse struct {
	Task   corequery.Task         `json:"task"`
	Plan   *approval.PlanDocument `json:"plan,omitempty"`
	PlanID kernel.PlanID          `json:"plan_id,omitempty"`
	RunID  kernel.RunID           `json:"run_id,omitempty"`
}

func (a *API) handleSessions(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	request, ok := parseListRequest(w, r)
	if !ok {
		return
	}
	items, err := a.queries.ListSessions(r.Context(), corequery.SessionListOptions{
		Page: request.Page, Sort: request.Sort,
	})
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": items, "page": request.metadata(len(items))})
}

func (a *API) handleSession(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r.URL.Path, "/v1/sessions/")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := a.queries.GetSession(r.Context(), kernel.SessionID(id))
		if errors.Is(err, corequery.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "session not found")
			return
		}
		if err != nil {
			writeInternal(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPatch:
		a.handleSessionPatch(w, r, kernel.SessionID(id))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PATCH")
	}
}

func (a *API) handleSessionPatch(w http.ResponseWriter, r *http.Request, id kernel.SessionID) {
	if a.sessionPrefs == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "session preferences unavailable")
		return
	}
	var body struct {
		PreferredModel   *string `json:"preferred_model"`
		PermissionStance *string `json:"permission_stance"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if body.PreferredModel == nil && body.PermissionStance == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "preferred_model or permission_stance is required")
		return
	}
	if body.PreferredModel != nil {
		if err := a.sessionPrefs.SetPreferredModel(r.Context(), id, *body.PreferredModel); err != nil {
			if errors.Is(err, kernel.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "session not found")
				return
			}
			if errors.Is(err, kernel.ErrInvalidAggregate) {
				writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
				return
			}
			writeInternal(w, err)
			return
		}
	}
	if body.PermissionStance != nil {
		if err := a.sessionPrefs.SetPermissionStance(r.Context(), id, *body.PermissionStance); err != nil {
			if errors.Is(err, kernel.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "session not found")
				return
			}
			if errors.Is(err, kernel.ErrInvalidAggregate) {
				writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
				return
			}
			writeInternal(w, err)
			return
		}
	}
	item, err := a.queries.GetSession(r.Context(), id)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *API) handleSessionMessages(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	// path: /v1/sessions/{id}/messages
	trimmed := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	id := strings.TrimSuffix(trimmed, "/messages")
	id = strings.Trim(id, "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "session id required")
		return
	}
	request, ok := parseListRequest(w, r)
	if !ok {
		return
	}
	items, err := a.queries.SessionTranscript(r.Context(), kernel.SessionID(id), corequery.TranscriptOptions{
		Page: request.Page,
	})
	if errors.Is(err, corequery.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": items, "page": request.metadata(len(items))})
}

func (a *API) handleSessionTodos(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	trimmed := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	id := strings.TrimSuffix(trimmed, "/todos")
	id = strings.Trim(id, "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "session id required")
		return
	}
	items, err := a.queries.ListSessionTodos(r.Context(), kernel.SessionID(id))
	if errors.Is(err, corequery.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"todos": items})
}

func (a *API) handleTaskMessages(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	trimmed := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
	id := strings.TrimSuffix(trimmed, "/messages")
	id = strings.Trim(id, "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "task id required")
		return
	}
	request, ok := parseListRequest(w, r)
	if !ok {
		return
	}
	items, err := a.queries.TaskTranscript(r.Context(), kernel.TaskID(id), corequery.TranscriptOptions{
		Page: request.Page,
	})
	if errors.Is(err, corequery.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "task not found")
		return
	}
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": items, "page": request.metadata(len(items))})
}

func (a *API) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.listTasks(w, r)
	case http.MethodPost:
		a.submitTask(w, r)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (a *API) listTasks(w http.ResponseWriter, r *http.Request) {
	request, ok := parseListRequest(w, r)
	if !ok {
		return
	}
	items, err := a.queries.ListTasks(r.Context(), corequery.TaskListOptions{
		Page: request.Page, Sort: request.Sort, State: strings.TrimSpace(r.URL.Query().Get("state")),
	})
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": items, "page": request.metadata(len(items))})
}

func (a *API) submitTask(w http.ResponseWriter, r *http.Request) {
	var request taskSubmissionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid task submission request")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "task submission request must contain one JSON value")
		return
	}
	allowExisting := strings.TrimSpace(string(request.TaskID)) != ""
	mode := kernel.NormalizeExecutionMode(string(request.ExecutionMode))
	slog.Info("task submit accepted", runlog.Attrs("gateway", "submit", "started", runlog.IDs{
		SessionID: string(request.SessionID), TaskID: string(request.TaskID),
	}, "execution_mode", string(mode), "allow_existing", allowExisting)...)
	result, err := a.taskSubmissions.Submit(r.Context(), tasksubmission.Request{
		TaskID: request.TaskID, SessionID: request.SessionID, PlanID: request.PlanID,
		Title: request.Title, Objective: request.Objective, SkillIDs: request.SkillIDs,
		ExecutionMode: request.ExecutionMode, Workspace: strings.TrimSpace(request.Workspace),
		PermissionStance: strings.TrimSpace(request.PermissionStance),
		PreferredModel:   strings.TrimSpace(request.PreferredModel),
		Interactive:      request.Interactive,
		EnsureSession:    true, AllowExisting: allowExisting,
	})
	if err != nil {
		slog.Error("task submit failed", runlog.Attrs("gateway", "submit", "failed", runlog.IDs{
			SessionID: string(request.SessionID), TaskID: string(request.TaskID),
		}, "error", err)...)
		if !writeApplicationError(w, err) {
			writeInternal(w, err)
		}
		return
	}
	slog.Info("task submit completed", runlog.Attrs("gateway", "submit", "succeeded", runlog.IDs{
		SessionID: string(result.Task.SessionID), TaskID: string(result.Task.ID),
		RunID: string(result.RunID), PlanID: string(result.PlanID),
	}, "execution_mode", string(result.Task.ExecutionMode))...)
	writeJSON(w, http.StatusCreated, taskSubmissionResponse{
		Task: taskViewFromDomain(result.Task), Plan: result.Plan, RunID: result.RunID, PlanID: result.PlanID,
	})
}

func (a *API) handleTask(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	id, ok := pathID(w, r.URL.Path, "/v1/tasks/")
	if !ok {
		return
	}
	item, err := a.queries.GetTask(r.Context(), kernel.TaskID(id))
	if errors.Is(err, corequery.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "task not found")
		return
	}
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *API) handleTaskUsage(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	basePath := strings.TrimSuffix(r.URL.Path, "/usage")
	id, ok := pathID(w, basePath, "/v1/tasks/")
	if !ok {
		return
	}
	usage, err := a.queries.TaskUsage(r.Context(), kernel.TaskID(id))
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, usage)
}

func (a *API) handleTaskContext(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	basePath := strings.TrimSuffix(r.URL.Path, "/context")
	id, ok := pathID(w, basePath, "/v1/tasks/")
	if !ok {
		return
	}
	item, err := a.queries.TaskContext(r.Context(), kernel.TaskID(id))
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *API) handleSessionContext(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	basePath := strings.TrimSuffix(r.URL.Path, "/context")
	id, ok := pathID(w, basePath, "/v1/sessions/")
	if !ok {
		return
	}
	item, err := a.queries.SessionContext(r.Context(), kernel.SessionID(id))
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *API) handleQuestions(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if a.userQuestions == nil {
		writeJSON(w, http.StatusOK, map[string]any{"questions": []UserQuestionView{}})
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := a.userQuestions.ListPending(r.Context(), sessionID, limit)
	if err != nil {
		writeInternal(w, err)
		return
	}
	if items == nil {
		items = []UserQuestionView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"questions": items})
}

func (a *API) handleQuestionAnswer(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if a.userQuestions == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "user questions are unavailable")
		return
	}
	basePath := strings.TrimSuffix(r.URL.Path, "/answer")
	id, ok := pathID(w, basePath, "/v1/questions/")
	if !ok {
		return
	}
	var request struct {
		Answers map[string][]string `json:"answers"`
		Actor   string              `json:"actor"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := a.userQuestions.Answer(r.Context(), id, request.Actor, request.Answers)
	if err != nil {
		if writeApplicationError(w, err) {
			return
		}
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *API) handleQuestionDismiss(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if a.userQuestions == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "user questions are unavailable")
		return
	}
	basePath := strings.TrimSuffix(r.URL.Path, "/dismiss")
	id, ok := pathID(w, basePath, "/v1/questions/")
	if !ok {
		return
	}
	var request struct {
		Actor string `json:"actor"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := a.userQuestions.Dismiss(r.Context(), id, request.Actor)
	if err != nil {
		if writeApplicationError(w, err) {
			return
		}
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *API) handlePermissions(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if a.toolPermissions == nil {
		writeJSON(w, http.StatusOK, map[string]any{"permissions": []ToolPermissionView{}})
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := a.toolPermissions.ListPending(r.Context(), sessionID, limit)
	if err != nil {
		writeInternal(w, err)
		return
	}
	if items == nil {
		items = []ToolPermissionView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": items})
}

func (a *API) handlePermissionDecide(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if a.toolPermissions == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "tool permissions are unavailable")
		return
	}
	basePath := strings.TrimSuffix(r.URL.Path, "/decide")
	id, ok := pathID(w, basePath, "/v1/permissions/")
	if !ok {
		return
	}
	var request struct {
		Decision string `json:"decision"`
		Actor    string `json:"actor"`
		Confirm  bool   `json:"confirm"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := a.toolPermissions.DecideWithConfirm(r.Context(), id, request.Decision, request.Actor, request.Confirm)
	if err != nil {
		slog.Warn("permission decide failed", runlog.Attrs("gateway", "permission_decide", "failed", runlog.IDs{},
			"permission_id", id, "decision", request.Decision, "error", err)...)
		msg := err.Error()
		if strings.Contains(msg, "not found") {
			writeError(w, http.StatusNotFound, "not_found", msg)
			return
		}
		if strings.Contains(msg, "not pending") || strings.Contains(msg, "invalid") {
			writeError(w, http.StatusBadRequest, "invalid_request", msg)
			return
		}
		writeInternal(w, err)
		return
	}
	slog.Info("permission decide completed", runlog.Attrs("gateway", "permission_decide", "succeeded", runlog.IDs{
		SessionID: item.SessionID, TaskID: item.TaskID, RunID: item.RunID,
	}, "permission_id", id, "decision", request.Decision, "tool", item.ToolName)...)
	writeJSON(w, http.StatusOK, item)
}

func (a *API) handleSessionCompact(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if a.sessionCompact == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "session compaction is unavailable until chat is configured")
		return
	}
	basePath := strings.TrimSuffix(r.URL.Path, "/compact")
	id, ok := pathID(w, basePath, "/v1/sessions/")
	if !ok {
		return
	}
	var request struct {
		Focus string `json:"focus"`
	}
	// Empty body is allowed (no focus).
	if r.Body != nil {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
			return
		}
		if len(strings.TrimSpace(string(raw))) > 0 {
			if err := json.Unmarshal(raw, &request); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
				return
			}
		}
	}
	result, err := a.sessionCompact.ForceCompact(r.Context(), kernel.SessionID(id), request.Focus)
	if err != nil {
		slog.Warn("session compact failed", runlog.Attrs("gateway", "compact", "failed", runlog.IDs{
			SessionID: id,
		}, "error", err)...)
		if writeApplicationError(w, err) {
			return
		}
		msg := err.Error()
		if strings.Contains(msg, "not enough history") || strings.Contains(msg, "session id") {
			writeError(w, http.StatusBadRequest, "invalid_request", msg)
			return
		}
		if strings.Contains(msg, "disabled") || strings.Contains(msg, "unavailable") {
			writeError(w, http.StatusServiceUnavailable, "unavailable", msg)
			return
		}
		writeInternal(w, err)
		return
	}
	slog.Info("session compact completed", runlog.Attrs("gateway", "compact", "succeeded", runlog.IDs{
		SessionID: id,
	})...)
	writeJSON(w, http.StatusOK, result)
}

func (a *API) handleSessionRewind(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if a.sessionRewind == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "session rewind is unavailable")
		return
	}
	basePath := strings.TrimSuffix(r.URL.Path, "/rewind")
	id, ok := pathID(w, basePath, "/v1/sessions/")
	if !ok {
		return
	}
	var request struct {
		RevisionID string `json:"revision_id"`
	}
	if r.Body != nil {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
			return
		}
		if len(strings.TrimSpace(string(raw))) > 0 {
			if err := json.Unmarshal(raw, &request); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
				return
			}
		}
	}
	result, err := a.sessionRewind.RewindEdit(r.Context(), kernel.SessionID(id), request.RevisionID)
	if err != nil {
		if writeApplicationError(w, err) {
			return
		}
		msg := err.Error()
		if strings.Contains(msg, "changed since") || strings.Contains(msg, "no edit") {
			writeError(w, http.StatusConflict, "conflict", msg)
			return
		}
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) handleSessionRetract(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if a.sessionRetract == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "session retract is unavailable")
		return
	}
	basePath := strings.TrimSuffix(r.URL.Path, "/retract")
	id, ok := pathID(w, basePath, "/v1/sessions/")
	if !ok {
		return
	}
	var request struct {
		RewindFiles bool `json:"rewind_files"`
	}
	if r.Body != nil {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
			return
		}
		if len(strings.TrimSpace(string(raw))) > 0 {
			if err := json.Unmarshal(raw, &request); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
				return
			}
		}
	}
	result, err := a.sessionRetract.RetractLastTurn(r.Context(), kernel.SessionID(id), request.RewindFiles)
	if err != nil {
		if writeApplicationError(w, err) {
			return
		}
		msg := err.Error()
		if strings.Contains(msg, "no turn") || strings.Contains(msg, "session id") {
			writeError(w, http.StatusBadRequest, "invalid_request", msg)
			return
		}
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) handleSessionSteer(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if a.sessionSteer == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "session steer is unavailable until chat is configured")
		return
	}
	basePath := strings.TrimSuffix(r.URL.Path, "/steer")
	id, ok := pathID(w, basePath, "/v1/sessions/")
	if !ok {
		return
	}
	var request struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := a.sessionSteer.Steer(r.Context(), kernel.SessionID(id), request.Text)
	if err != nil {
		slog.Warn("session steer failed", runlog.Attrs("gateway", "steer", "failed", runlog.IDs{
			SessionID: id,
		}, "error", err)...)
		if writeApplicationError(w, err) {
			return
		}
		writeInternal(w, err)
		return
	}
	slog.Info("session steer accepted", runlog.Attrs("gateway", "steer", "succeeded", runlog.IDs{
		SessionID: id, TaskID: string(result.TaskID), RunID: string(result.RunID),
	})...)
	writeJSON(w, http.StatusAccepted, result)
}

func (a *API) handleTaskAction(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if a.taskControls == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "task controls are unavailable until a Provider is configured")
		return
	}
	basePath := strings.TrimSuffix(r.URL.Path, "/actions")
	id, ok := pathID(w, basePath, "/v1/tasks/")
	if !ok {
		return
	}
	var request taskcontrol.TaskActionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.TaskID = kernel.TaskID(id)
	task, err := a.taskControls.ControlTask(r.Context(), request)
	if err != nil {
		slog.Warn("task action failed", runlog.Attrs("gateway", "task_action", "failed", runlog.IDs{
			TaskID: id,
		}, "action", string(request.Action), "error", err)...)
		if !writeApplicationError(w, err) {
			writeInternal(w, err)
		}
		return
	}
	slog.Info("task action completed", runlog.Attrs("gateway", "task_action", "succeeded", runlog.IDs{
		SessionID: string(task.SessionID), TaskID: string(task.ID),
	}, "action", string(request.Action), "task_state", string(task.State))...)
	writeJSON(w, http.StatusOK, taskViewFromDomain(task))
}

type jobActionRequest struct {
	Action   string `json:"action"`
	Reviewer string `json:"reviewer,omitempty"`
	Reason   string `json:"reason"`
}
