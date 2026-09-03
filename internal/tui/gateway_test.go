package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
	"github.com/yyZe0122/yunmengze-agent/internal/modelstream"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
	"github.com/yyZe0122/yunmengze-agent/pkg/eventapi"
	"github.com/yyZe0122/yunmengze-agent/pkg/schedulerapi"
)

// fakeGateway is a minimal Gateway for unit tests.
type fakeGateway struct {
	tasks            []gatewayclient.Task
	jobs             []schedulerapi.Job
	skills           []gatewayclient.Skill
	commands         []gatewayclient.ChatCommand
	health           gatewayclient.Health
	model            gatewayclient.ModelConfig
	sessionPreferred string
	sessionExists    bool
	controlCalls     []gatewayclient.TaskAction
	controlErr       error
	permissions      []gatewayclient.Permission
	steers           []string
	steerErr         error
	submits          []gatewayclient.TaskSubmissionRequest
	submitOK         bool
	questions        []gatewayclient.UserQuestion
	todos            []gatewayclient.SessionTodo
	retract          gatewayclient.RetractResult
	retractErr       error
}

func (f *fakeGateway) StreamEvents(context.Context, uint64, func(eventapi.Envelope) error) error {
	return context.Canceled
}

func (f *fakeGateway) StreamModelEvents(context.Context, gatewayclient.SessionID, gatewayclient.RunID, func(modelstream.Envelope) error) error {
	return context.Canceled
}

func (f *fakeGateway) ListSessions(context.Context, int) ([]gatewayclient.Session, error) {
	return nil, nil
}

func (f *fakeGateway) GetSession(_ context.Context, id gatewayclient.SessionID) (gatewayclient.Session, error) {
	if !f.sessionExists && f.sessionPreferred == "" {
		return gatewayclient.Session{ID: id}, nil
	}
	return gatewayclient.Session{ID: id, PreferredModel: f.sessionPreferred}, nil
}

func (f *fakeGateway) SetSessionPreferredModel(_ context.Context, id gatewayclient.SessionID, model string) (gatewayclient.Session, error) {
	f.sessionPreferred = strings.TrimSpace(model)
	f.sessionExists = true
	return gatewayclient.Session{ID: id, PreferredModel: f.sessionPreferred}, nil
}

func (f *fakeGateway) SetSessionPermissionStance(_ context.Context, id gatewayclient.SessionID, stance string) (gatewayclient.Session, error) {
	return gatewayclient.Session{ID: id, PermissionStance: strings.TrimSpace(stance)}, nil
}

func (f *fakeGateway) SessionMessages(context.Context, gatewayclient.SessionID, int) ([]gatewayclient.TranscriptMessage, error) {
	return nil, nil
}

func (f *fakeGateway) ListSessionTodos(context.Context, gatewayclient.SessionID) ([]gatewayclient.SessionTodo, error) {
	return f.todos, nil
}

func (f *fakeGateway) CompactSession(context.Context, gatewayclient.SessionID, string) (gatewayclient.CompactResult, error) {
	return gatewayclient.CompactResult{Source: "llm"}, nil
}

func (f *fakeGateway) SteerSession(_ context.Context, _ gatewayclient.SessionID, text string) (gatewayclient.SteerResult, error) {
	f.steers = append(f.steers, strings.TrimSpace(text))
	if f.steerErr != nil {
		return gatewayclient.SteerResult{}, f.steerErr
	}
	return gatewayclient.SteerResult{SessionID: "session-1", TaskID: "task-1", RunID: "run-1", ItemID: "steer-1"}, nil
}

func (f *fakeGateway) RewindSession(context.Context, gatewayclient.SessionID, string) (gatewayclient.RewindResult, error) {
	return gatewayclient.RewindResult{Path: "/tmp/ws/a.go"}, nil
}

func (f *fakeGateway) RetractSession(_ context.Context, _ gatewayclient.SessionID, rewindFiles bool) (gatewayclient.RetractResult, error) {
	if f.retractErr != nil {
		return f.retract, f.retractErr
	}
	out := f.retract
	if out.UserText == "" {
		out.UserText = "hello again"
	}
	if out.TaskID == "" {
		out.TaskID = "task-last"
	}
	out.RewindFiles = rewindFiles
	return out, nil
}

func (f *fakeGateway) TaskMessages(context.Context, gatewayclient.TaskID, int) ([]gatewayclient.TranscriptMessage, error) {
	return nil, nil
}

func (f *fakeGateway) Health(context.Context) (gatewayclient.Health, error) {
	return f.health, nil
}

func (f *fakeGateway) ModelConfig(context.Context) (gatewayclient.ModelConfig, error) {
	return f.model, nil
}

func (f *fakeGateway) SetModelConfig(_ context.Context, model string) (gatewayclient.ModelConfig, error) {
	f.model.Model = model
	return f.model, nil
}

func (f *fakeGateway) MCPStatus(context.Context) (gatewayclient.MCPStatus, error) {
	return gatewayclient.MCPStatus{}, nil
}

func (f *fakeGateway) ListSkills(context.Context) ([]gatewayclient.Skill, error) {
	return f.skills, nil
}

func (f *fakeGateway) ListSkillsFilter(_ context.Context, includeArchived bool) ([]gatewayclient.Skill, error) {
	if !includeArchived {
		return f.skills, nil
	}
	var out []gatewayclient.Skill
	for _, s := range f.skills {
		if s.ArchivedAt != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeGateway) ListSkillEvents(context.Context, string, int) ([]gatewayclient.SkillEvent, error) {
	return nil, nil
}

func (f *fakeGateway) ApplySkillDraft(context.Context, string) error { return nil }

func (f *fakeGateway) RejectSkillDraft(context.Context, string) error { return nil }

func (f *fakeGateway) ListChatCommands(context.Context) ([]gatewayclient.ChatCommand, error) {
	return f.commands, nil
}

func (f *fakeGateway) ListTasks(context.Context, int) ([]gatewayclient.Task, error) {
	return f.tasks, nil
}

func (f *fakeGateway) GetTask(_ context.Context, id gatewayclient.TaskID) (gatewayclient.Task, error) {
	for _, task := range f.tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return gatewayclient.Task{}, errors.New("not found")
}

func (f *fakeGateway) TaskUsage(_ context.Context, id gatewayclient.TaskID) (gatewayclient.TaskUsage, error) {
	return gatewayclient.TaskUsage{TaskID: id}, nil
}

func (f *fakeGateway) TaskContext(_ context.Context, id gatewayclient.TaskID) (gatewayclient.TaskContext, error) {
	return gatewayclient.TaskContext{TaskID: id, Source: "none", Ratio: 1}, nil
}

func (f *fakeGateway) SubmitTask(_ context.Context, req gatewayclient.TaskSubmissionRequest) (gatewayclient.TaskSubmissionResponse, error) {
	f.submits = append(f.submits, req)
	if !f.submitOK {
		return gatewayclient.TaskSubmissionResponse{}, errors.New("not implemented")
	}
	sid := gatewayclient.SessionID(req.SessionID)
	task := gatewayclient.Task{
		ID: "task-new", Title: req.Title, Objective: req.Objective,
		State: gatewayclient.TaskStateRunning,
	}
	if sid != "" {
		task.SessionID = &sid
	}
	return gatewayclient.TaskSubmissionResponse{Task: task}, nil
}

func (f *fakeGateway) ControlTask(_ context.Context, id gatewayclient.TaskID, action gatewayclient.TaskAction, _ uint64, _ string) (gatewayclient.Task, error) {
	f.controlCalls = append(f.controlCalls, action)
	if f.controlErr != nil {
		return gatewayclient.Task{}, f.controlErr
	}
	for i := range f.tasks {
		if f.tasks[i].ID == id {
			f.tasks[i].State = gatewayclient.TaskStateCancelled
			return f.tasks[i], nil
		}
	}
	return gatewayclient.Task{ID: id, State: gatewayclient.TaskStateCancelled}, nil
}

func (f *fakeGateway) GetPlan(context.Context, gatewayclient.PlanID) (gatewayclient.Plan, error) {
	return gatewayclient.Plan{}, errors.New("not found")
}

func (f *fakeGateway) FindPlanForTask(context.Context, gatewayclient.TaskID) (gatewayclient.Plan, error) {
	return gatewayclient.Plan{}, errors.New("not found")
}

func (f *fakeGateway) ListRuns(context.Context, gatewayclient.TaskID, int) ([]gatewayclient.Run, error) {
	return nil, nil
}

func (f *fakeGateway) RunUsage(_ context.Context, id gatewayclient.RunID) (gatewayclient.RunUsage, error) {
	return gatewayclient.RunUsage{RunID: id}, nil
}

func (f *fakeGateway) ListJobs(context.Context, bool) ([]schedulerapi.Job, error) {
	return f.jobs, nil
}

func (f *fakeGateway) ListPermissions(context.Context, string, int) ([]gatewayclient.Permission, error) {
	return f.permissions, nil
}

func (f *fakeGateway) DecidePermission(context.Context, string, string) (gatewayclient.Permission, error) {
	return gatewayclient.Permission{}, nil
}

func (f *fakeGateway) DecidePermissionConfirm(context.Context, string, string, bool) (gatewayclient.Permission, error) {
	return gatewayclient.Permission{}, nil
}

func (f *fakeGateway) ListQuestions(context.Context, string, int) ([]gatewayclient.UserQuestion, error) {
	return f.questions, nil
}

func (f *fakeGateway) AnswerQuestion(_ context.Context, id, _ string, answers map[string][]string) (gatewayclient.UserQuestion, error) {
	return gatewayclient.UserQuestion{ID: id, State: "answered", Answers: answers}, nil
}

func (f *fakeGateway) DismissQuestion(_ context.Context, id, _ string) (gatewayclient.UserQuestion, error) {
	return gatewayclient.UserQuestion{ID: id, State: "unavailable"}, nil
}

func (f *fakeGateway) ListMemory(context.Context, string, string, string, int) ([]gatewayclient.MemoryEntry, error) {
	return nil, nil
}

func (f *fakeGateway) ListMemoryFilter(context.Context, string, string, string, int, bool) ([]gatewayclient.MemoryEntry, error) {
	return nil, nil
}

func (f *fakeGateway) RefreshMemory(context.Context, string) error { return nil }

func (f *fakeGateway) ForgetMemory(context.Context, string) error { return nil }

func (f *fakeGateway) PromoteMemory(context.Context, string) (gatewayclient.MemoryEntry, error) {
	return gatewayclient.MemoryEntry{}, nil
}

func (f *fakeGateway) CreateJob(_ context.Context, request schedulerapi.CreateRequest) (schedulerapi.Job, error) {
	job := schedulerapi.Job{
		ID: "job-created", Name: request.Name, SessionID: request.SessionID,
		TaskTitle: request.TaskTitle, TaskObjective: request.TaskObjective,
		ExecutionMode: request.ExecutionMode, SkillIDs: request.SkillIDs,
		IntervalSeconds: request.IntervalSeconds, Status: schedulerapi.StatusActive,
	}
	f.jobs = append(f.jobs, job)
	return job, nil
}

func TestRefreshCmdUsesGateway(t *testing.T) {
	gw := &fakeGateway{
		tasks: []gatewayclient.Task{{
			ID: "task-1", Title: "t", State: gatewayclient.TaskStateRunning, Version: 1,
		}},
		health: gatewayclient.Health{OK: true},
		model:  gatewayclient.ModelConfig{Model: "p/m", Models: []string{"p/m"}},
	}
	gw.health.Core.Runtime.DataDir = "/tmp/ymz-data"

	m := newModel(paths.ModeUser, gw)
	msg := m.refreshCmd(1, refreshFull)()
	done, ok := msg.(refreshDoneMsg)
	if !ok {
		t.Fatalf("msg type %T", msg)
	}
	if done.err != nil {
		t.Fatal(done.err)
	}
	if len(done.tasks) != 1 || done.tasks[0].ID != "task-1" {
		t.Fatalf("tasks = %#v", done.tasks)
	}

	statusMsg := m.loadStatusCmd()()
	status, ok := statusMsg.(statusDoneMsg)
	if !ok {
		t.Fatalf("status type %T", statusMsg)
	}
	if status.err != nil || !status.health.OK || status.model.Model != "p/m" {
		t.Fatalf("status = %#v", status)
	}
	if status.health.Core.Runtime.DataDir != "/tmp/ymz-data" {
		t.Fatalf("dataDir = %q", status.health.Core.Runtime.DataDir)
	}
}

func TestModelUpdateRefresh(t *testing.T) {
	gw := &fakeGateway{
		tasks: []gatewayclient.Task{{
			ID: "task-1", Title: "hello", State: gatewayclient.TaskStateCompleted, Version: 2,
		}},
	}
	m := newModel(paths.ModeUser, gw)
	m.width, m.height = 100, 40
	updated, _ := m.Update(refreshDoneMsg{tasks: gw.tasks})
	mm, ok := updated.(model)
	if !ok {
		t.Fatalf("type %T", updated)
	}
	if len(mm.tasks) != 1 || mm.tasks[0].Title != "hello" {
		t.Fatalf("tasks = %#v", mm.tasks)
	}
}

func TestModelListAndCron(t *testing.T) {
	gw := &fakeGateway{
		tasks: []gatewayclient.Task{{
			ID: "task-abc", Title: "one", State: gatewayclient.TaskStateRunning, Version: 1,
		}},
		jobs: []schedulerapi.Job{{
			ID: "job-1", Name: "heartbeat", Status: schedulerapi.StatusActive,
			NextRunAt: "2026-07-30T12:00:00Z", IntervalSeconds: 60,
		}},
		model: gatewayclient.ModelConfig{
			Model:  "deepseek/a",
			Models: []string{"deepseek/a", "deepseek/b"},
		},
		health: gatewayclient.Health{OK: true},
	}
	gw.health.Core.Runtime.DataDir = "/data/ymz"

	m := newModel(paths.ModeUser, gw)
	m.width, m.height = 120, 40

	updated, _ := m.Update(statusDoneMsg{health: gw.health, model: gw.model})
	mm := updated.(model)
	if mm.dataDir != "/data/ymz" || mm.modelName != "deepseek/a" || len(mm.models) != 2 {
		t.Fatalf("status fields: dataDir=%q model=%q models=%v", mm.dataDir, mm.modelName, mm.models)
	}

	updated, _ = mm.Update(commandDoneMsg{
		openList: listModels, modelName: gw.model.Model, models: gw.model.Models, status: "select a model",
	})
	mm = updated.(model)
	if mm.list != listModels || mm.listLen() != 2 {
		t.Fatalf("listModels: kind=%v len=%d", mm.list, mm.listLen())
	}
	view := renderPickerOverlay(&mm, 80)
	for _, want := range []string{"Models", "deepseek/a", "deepseek/b"} {
		if !strings.Contains(view, want) {
			t.Fatalf("model list missing %q:\n%s", want, view)
		}
	}

	msg := mm.cronCmd("")()
	done := msg.(commandDoneMsg)
	if done.err != nil || done.openList != listJobs || len(done.jobs) != 1 {
		t.Fatalf("cronCmd = %#v", done)
	}
	updated, _ = mm.Update(done)
	mm = updated.(model)
	if mm.list != listJobs || mm.listLen() != 1 {
		t.Fatalf("listJobs: kind=%v len=%d", mm.list, mm.listLen())
	}

	updated, _ = mm.Update(commandDoneMsg{clearTask: true, openList: listSessions, status: "sessions"})
	mm = updated.(model)
	if mm.list != listSessions || mm.task != nil {
		t.Fatalf("sessions: list=%v task=%v", mm.list, mm.task)
	}
	tid := gatewayclient.TaskID("task-abc")
	mm.sessions = []gatewayclient.Session{{
		ID: "session-1", Title: "one", LatestTaskID: &tid, LatestState: gatewayclient.TaskStateRunning,
	}}
	view = renderPickerOverlay(&mm, 80)
	for _, want := range []string{"Sessions", "session-1", "one"} {
		if !strings.Contains(view, want) {
			t.Fatalf("sessions missing %q:\n%s", want, view)
		}
	}

	// Skills multi-select picker.
	gw.skills = []gatewayclient.Skill{
		{ID: "git", Name: "Git", Description: "git helpers", Source: "user"},
		{ID: "go", Name: "Go", Description: "go helpers", Source: "project"},
	}
	skillMsg := mm.skillsCmd("")()
	skillDone := skillMsg.(commandDoneMsg)
	if skillDone.err != nil || skillDone.openList != listSkills || len(skillDone.skills) != 2 {
		t.Fatalf("skillsCmd = %#v", skillDone)
	}
	updated, _ = mm.Update(skillDone)
	mm = updated.(model)
	if mm.list != listSkills || mm.listLen() != 2 {
		t.Fatalf("listSkills: kind=%v len=%d", mm.list, mm.listLen())
	}
	mm.selectedIdx = 0
	_ = mm.listEnter()
	if len(mm.selectedSkillIDs) != 1 || mm.selectedSkillIDs[0] != "git" {
		t.Fatalf("after toggle select = %#v", mm.selectedSkillIDs)
	}
	_ = mm.listEnter()
	if len(mm.selectedSkillIDs) != 0 {
		t.Fatalf("after toggle off = %#v", mm.selectedSkillIDs)
	}
	mm.selectedIdx = 1
	_ = mm.listEnter()
	if len(mm.selectedSkillIDs) != 1 || mm.selectedSkillIDs[0] != "go" {
		t.Fatalf("select go = %#v", mm.selectedSkillIDs)
	}
	view = renderPickerOverlay(&mm, 80)
	for _, want := range []string{"Skills", "git", "go"} {
		if !strings.Contains(view, want) {
			t.Fatalf("skills list missing %q:\n%s", want, view)
		}
	}

	// Skill-as-slash: /git toggles; /go msg selects and queues submit.
	mm.selectedSkillIDs = nil
	toggleMsg := mm.handleLineCmd("/git")()
	toggleDone := toggleMsg.(commandDoneMsg)
	if toggleDone.err != nil || len(toggleDone.skillIDs) != 1 || toggleDone.skillIDs[0] != "git" {
		t.Fatalf("skill slash toggle = %#v", toggleDone)
	}
	updated, _ = mm.Update(toggleDone)
	mm = updated.(model)
	if len(mm.selectedSkillIDs) != 1 || mm.selectedSkillIDs[0] != "git" {
		t.Fatalf("after /git = %#v", mm.selectedSkillIDs)
	}
	submitMsg := mm.handleLineCmd("/go do work")()
	submitDone := submitMsg.(commandDoneMsg)
	if submitDone.err != nil || submitDone.submitAfter != "do work" {
		t.Fatalf("skill slash submit = %#v", submitDone)
	}
	if len(submitDone.skillIDs) < 1 {
		t.Fatalf("expected skill ids on submit path: %#v", submitDone.skillIDs)
	}
	foundGo := false
	for _, id := range submitDone.skillIDs {
		if id == "go" {
			foundGo = true
		}
	}
	if !foundGo {
		t.Fatalf("expected go in skillIDs: %#v", submitDone.skillIDs)
	}

	strip := mm.renderContextStrip()
	if !strings.Contains(strip, "deepseek/a") || !strings.Contains(strip, "ctx") {
		t.Fatalf("strip=%q", strip)
	}
	panel := mm.renderMetricsPanel(20)
	for _, want := range []string{"context", "tokens", "data", "/data/ymz"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("panel missing %q:\n%s", want, panel)
		}
	}
	if strings.Contains(panel, "MCP") || strings.Contains(panel, "cache") {
		t.Fatalf("panel should hide cache/MCP without data:\n%s", panel)
	}
}
