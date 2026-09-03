package kernel

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var testTime = time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)

func TestNormalizePermissionStance(t *testing.T) {
	t.Parallel()
	got, err := NormalizePermissionStance("")
	if err != nil || got != PermissionStanceAgent {
		t.Fatalf("empty = %q %v", got, err)
	}
	got, err = NormalizePermissionStance("AUTO")
	if err != nil || got != PermissionStanceAuto {
		t.Fatalf("auto = %q %v", got, err)
	}
	if _, err := NormalizePermissionStance("yolo"); !errors.Is(err, ErrInvalidAggregate) {
		t.Fatalf("yolo err = %v", err)
	}
}

func TestSessionMetadataEncodeKeepsAllFields(t *testing.T) {
	t.Parallel()
	raw := sessionMetadataEncode("/tmp/ws", "deepseek1/deepseek-chat", PermissionStanceAuto, []string{"task-hidden"}, []string{"/tmp/extra"})
	if workspaceFromMetadata(raw) != "/tmp/ws" {
		t.Fatalf("workspace = %q", workspaceFromMetadata(raw))
	}
	if preferredModelFromMetadata(raw) != "deepseek1/deepseek-chat" {
		t.Fatalf("model = %q", preferredModelFromMetadata(raw))
	}
	if permissionStanceFromMetadata(raw) != PermissionStanceAuto {
		t.Fatalf("stance = %q", permissionStanceFromMetadata(raw))
	}
	if got := hiddenTaskIDsFromMetadata(raw); len(got) != 1 || got[0] != "task-hidden" {
		t.Fatalf("hidden = %#v", got)
	}
	if got := extraRootsFromMetadata(raw); len(got) != 1 || got[0] != "/tmp/extra" {
		t.Fatalf("extra = %#v", got)
	}
	agentOnly := sessionMetadataEncode("/tmp/ws", "deepseek1/deepseek-chat", PermissionStanceAgent, []string{"task-hidden"}, nil)
	if strings.Contains(agentOnly, "permission_stance") {
		t.Fatalf("agent stance should omit key: %s", agentOnly)
	}
	if !strings.Contains(agentOnly, "hidden_task_ids") {
		t.Fatalf("hidden ids dropped: %s", agentOnly)
	}
}

func TestTaskChatLifecycle(t *testing.T) {
	t.Parallel()

	task, err := NewTask("task-1", "session-1", "Build", "Build YunmengZe", testTime)
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	lifecycle := []TaskState{TaskRunning, TaskCompleted}
	for _, state := range lifecycle {
		if err := task.Transition(state, testTime.Add(time.Duration(task.Version)*time.Minute)); err != nil {
			t.Fatalf("Transition(%s) error = %v", state, err)
		}
	}
	if task.State != TaskCompleted || task.Version != 3 {
		t.Fatalf("task = %+v, want completed version 3", task)
	}
	if err := task.Transition(TaskRunning, testTime); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal transition error = %v, want ErrInvalidTransition", err)
	}
}

func TestTaskPauseResumeLifecycle(t *testing.T) {
	t.Parallel()

	task, err := NewTask("task-1", "session-1", "Observe", "Keep running", testTime)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []TaskState{TaskRunning, TaskPaused, TaskRunning, TaskCompleted} {
		if err := task.Transition(state, testTime.Add(time.Duration(task.Version)*time.Minute)); err != nil {
			t.Fatalf("Transition(%s) error = %v", state, err)
		}
	}
	if task.State != TaskCompleted {
		t.Fatalf("task state = %s, want %s", task.State, TaskCompleted)
	}

	paused, _ := NewTask("task-2", "session-1", "Pause", "Cancel while paused", testTime)
	for _, state := range []TaskState{TaskRunning, TaskPaused} {
		if err := paused.Transition(state, testTime); err != nil {
			t.Fatal(err)
		}
	}
	if err := paused.Cancel(testTime); err != nil {
		t.Fatalf("Cancel() from paused error = %v", err)
	}
	if paused.State != TaskCancelled {
		t.Fatalf("paused cancellation state = %s, want %s", paused.State, TaskCancelled)
	}
}

func TestTaskRejectsIllegalTransitionWithoutMutation(t *testing.T) {
	t.Parallel()

	task, err := NewTask("task-1", "session-1", "Build", "Build YunmengZe", testTime)
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	before := task
	if err := task.Transition(TaskCompleted, testTime.Add(time.Minute)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Transition() error = %v, want ErrInvalidTransition", err)
	}
	if task != before {
		t.Fatalf("illegal transition mutated task: before=%+v after=%+v", before, task)
	}
}

func TestTaskCreatedToRunningForAgentChat(t *testing.T) {
	t.Parallel()
	task, err := NewTask("task-chat", "session-1", "Hi", "你好", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := task.Transition(TaskRunning, testTime.Add(time.Minute)); err != nil {
		t.Fatalf("created→running: %v", err)
	}
	if task.State != TaskRunning {
		t.Fatalf("state = %s", task.State)
	}
}

func TestTaskRejectsUnknownStateTransition(t *testing.T) {
	t.Parallel()
	task, err := NewTask("task-1", "session-1", "No", "Planner", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := task.Transition(TaskState("planning"), testTime); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("created→planning: %v", err)
	}
}

func TestTaskCancelFromFailed(t *testing.T) {
	t.Parallel()

	failed, _ := NewTask("failed", "session", "Failed", "Retry", testTime)
	if err := failed.Transition(TaskRunning, testTime); err != nil {
		t.Fatal(err)
	}
	if err := failed.Transition(TaskFailed, testTime); err != nil {
		t.Fatal(err)
	}
	if err := failed.Cancel(testTime); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if failed.State != TaskCancelled {
		t.Fatalf("cancelled state = %s", failed.State)
	}
}

func TestSessionPlanStepAndRunStateMachines(t *testing.T) {
	t.Parallel()

	session, err := NewSession("session-1", testTime)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if err := session.Close(testTime.Add(time.Minute)); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := session.Close(testTime); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second Close() error = %v, want ErrInvalidTransition", err)
	}

	plan, err := NewPlan("plan-1", "task-1", 1, "scope-hash", testTime)
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	step, err := NewPlanStep("step-1", plan.ID, 0, "Inspect", "R0", testTime)
	if err != nil {
		t.Fatalf("NewPlanStep() error = %v", err)
	}
	if err := plan.AddStep(step, testTime); err != nil {
		t.Fatalf("AddStep() error = %v", err)
	}
	if err := plan.Transition(PlanState("waiting_approval"), testTime); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("draft→waiting_approval should be illegal: %v", err)
	}
	if err := plan.Transition(PlanApproved, testTime); err != nil {
		t.Fatalf("plan approval error = %v", err)
	}
	if err := plan.AddStep(step, testTime); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AddStep after approval error = %v, want ErrInvalidTransition", err)
	}

	if err := step.Transition(StepApproved, testTime); err != nil {
		t.Fatal(err)
	}
	if err := step.Transition(StepRunning, testTime); err != nil {
		t.Fatal(err)
	}
	if err := step.Transition(StepCompleted, testTime); err != nil {
		t.Fatal(err)
	}

	run, err := NewRun("run-1", "task-1", "plan-1", testTime)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	if err := run.Transition(RunRunning, "", testTime); err != nil {
		t.Fatal(err)
	}
	if err := run.Transition(RunCompleted, "", testTime.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if run.FinishedAt == nil || run.State != RunCompleted {
		t.Fatalf("completed run = %+v", run)
	}
}

func TestConstructorsRejectInvalidAggregates(t *testing.T) {
	t.Parallel()

	if _, err := NewSession("", testTime); !errors.Is(err, ErrInvalidAggregate) {
		t.Errorf("NewSession invalid error = %v", err)
	}
	if _, err := NewTask("", "session", "title", "objective", testTime); !errors.Is(err, ErrInvalidAggregate) {
		t.Errorf("NewTask invalid error = %v", err)
	}
	if _, err := NewPlan("plan", "task", 0, "hash", testTime); !errors.Is(err, ErrInvalidAggregate) {
		t.Errorf("NewPlan invalid error = %v", err)
	}
	if _, err := NewPlanStep("step", "plan", -1, "title", "R0", testTime); !errors.Is(err, ErrInvalidAggregate) {
		t.Errorf("NewPlanStep invalid error = %v", err)
	}
	if _, err := NewRun("run", "", "plan", testTime); !errors.Is(err, ErrInvalidAggregate) {
		t.Errorf("NewRun invalid error = %v", err)
	}
}
