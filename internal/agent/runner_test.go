package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/contextpack"
	coresqlite "github.com/yyZe0122/yunmengze-agent/internal/store/sqlite"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

func TestRunnerTrimsLongToolResultsForProviderOnly(t *testing.T) {
	store, db, _ := openAgentFixture(t)
	longBody := strings.Repeat("Z", 200)
	longOutputBytes, err := json.Marshal(map[string]string{"body": longBody})
	if err != nil {
		t.Fatal(err)
	}
	longOutput := string(longOutputBytes)
	provider := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{
			ToolCalls: []providerapi.ToolCall{{ID: "call-long", Name: "test_read", Arguments: `{}`}},
			Usage:     providerapi.Usage{TotalTokens: 1},
		},
		{Content: "done", Usage: providerapi.Usage{TotalTokens: 1}},
	}}
	broker := &longOutputBroker{recordingBroker: recordingBroker{db: db, definitions: testDefinitions()}, output: longOutput}
	runner, err := New(Config{
		Provider: provider, Broker: broker, Records: store, Model: "test-model",
		MaxToolResultRunes: 96,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := testRunRequest()
	request.AllowedTools = []string{"test_read"}
	result, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" {
		t.Fatalf("result = %+v", result)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(provider.requests))
	}
	// Second request includes tool result: trimmed for provider.
	var toolMsg *providerapi.Message
	for i := range provider.requests[1].Messages {
		m := &provider.requests[1].Messages[i]
		if m.Role == providerapi.RoleTool && m.ToolCallID == "call-long" {
			toolMsg = m
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("missing tool message in provider request")
	}
	if toolMsg.Content == longOutput {
		t.Fatal("provider still received full tool output")
	}
	if !strings.Contains(toolMsg.Content, "trimmed") {
		t.Fatalf("expected trim marker, got %q", toolMsg.Content)
	}
	// Persisted record keeps full content.
	records, err := store.List(context.Background(), request.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var persisted string
	for _, rec := range records {
		if rec.Type == RecordToolResult && rec.Message.ToolCallID == "call-long" {
			persisted = rec.Message.Content
			break
		}
	}
	if persisted != longOutput {
		t.Fatalf("persisted tool result len=%d want %d", len(persisted), len(longOutput))
	}
}

func TestRunnerRetriesRetryableProviderErrors(t *testing.T) {
	store, _, _ := openAgentFixture(t)
	provider := &sequenceProvider{
		errors: []error{
			&providerapi.ProviderError{Provider: "test", Kind: providerapi.ErrorUnavailable, Retryable: true, RetryAfter: time.Millisecond},
			&providerapi.ProviderError{Provider: "test", Kind: providerapi.ErrorUnavailable, Retryable: true, RetryAfter: time.Millisecond},
		},
		responses: []providerapi.CompletionResponse{{}, {}, {
			Content: "done", Usage: providerapi.Usage{TotalTokens: 3},
		}},
	}
	runner := newTestRunner(t, provider, &recordingBroker{}, store)

	result, err := runner.Run(context.Background(), testRunRequest())
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 3 {
		t.Fatalf("provider calls = %d, want 3", provider.calls)
	}
	if result.Iterations != 1 || result.Content != "done" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunnerDoesNotRetryNonRetryableProviderError(t *testing.T) {
	store, _, _ := openAgentFixture(t)
	want := &providerapi.ProviderError{Provider: "test", Kind: providerapi.ErrorInvalidRequest, Retryable: false}
	provider := &sequenceProvider{errors: []error{want}}
	runner := newTestRunner(t, provider, &recordingBroker{}, store)

	_, err := runner.Run(context.Background(), testRunRequest())
	if !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want %v", err, want)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
}

func TestRunnerEnforcesPersistedTokenBudget(t *testing.T) {
	store, _, _ := openAgentFixture(t)
	provider := &sequenceProvider{responses: []providerapi.CompletionResponse{{
		Content: "too expensive", Usage: providerapi.Usage{TotalTokens: 11},
	}}}
	request := testRunRequest()
	request.MaxOutputTokens = 10
	request.MaxTotalTokens = 10
	runner := newTestRunner(t, provider, &recordingBroker{}, store)

	result, err := runner.Run(context.Background(), request)
	if !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Fatalf("Run() error = %v, want ErrTokenBudgetExceeded", err)
	}
	if result.Usage.TotalTokens != 11 {
		t.Fatalf("usage = %+v", result.Usage)
	}

	// A restarted Runner must read the durable usage and refuse another
	// Provider call instead of treating the over-budget final record as success.
	restartedProvider := &sequenceProvider{responses: []providerapi.CompletionResponse{{Content: "should not run"}}}
	restarted := newTestRunner(t, restartedProvider, &recordingBroker{}, store)
	_, err = restarted.Run(context.Background(), request)
	if !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Fatalf("restarted Run() error = %v, want ErrTokenBudgetExceeded", err)
	}
	if restartedProvider.calls != 0 {
		t.Fatalf("restarted provider calls = %d, want 0", restartedProvider.calls)
	}
}

func TestRunnerStopsBeforeToolsWhenCostBudgetIsExceeded(t *testing.T) {
	store, db, _ := openAgentFixture(t)
	provider := &sequenceProvider{responses: []providerapi.CompletionResponse{{
		ToolCalls: []providerapi.ToolCall{{ID: "call-budget", Name: "test_read", Arguments: `{}`}},
		Usage:     providerapi.Usage{TotalTokens: 2, Cost: providerapi.Cost{Currency: "USD", Micros: 101}},
	}}}
	broker := &recordingBroker{db: db, definitions: testDefinitions()}
	request := testRunRequest()
	request.AllowedTools = []string{"test_read"}
	request.MaxCostMicros = 100
	runner := newTestRunner(t, provider, broker, store)

	_, err := runner.Run(context.Background(), request)
	if !errors.Is(err, ErrCostBudgetExceeded) {
		t.Fatalf("Run() error = %v, want ErrCostBudgetExceeded", err)
	}
	if broker.calls != 0 {
		t.Fatalf("tool calls = %d, want 0", broker.calls)
	}
}

func TestSelectDefinitionsDedupsAllowedTools(t *testing.T) {
	defs, advertised, err := selectDefinitions(testDefinitions(), []string{"test_read", "test_read", " test_read "})
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].Name != "test_read" {
		t.Fatalf("defs = %+v", defs)
	}
	if _, ok := advertised["test_read"]; !ok || len(advertised) != 1 {
		t.Fatalf("advertised = %#v", advertised)
	}
}

func TestRunnerFeedsFailedToolResultAndContinues(t *testing.T) {
	store, _, _ := openAgentFixture(t)
	provider := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{
			ToolCalls: []providerapi.ToolCall{{ID: "call-fail", Name: "test_read", Arguments: `{}`}},
			Usage:     providerapi.Usage{TotalTokens: 2},
		},
		{Content: "fixed after failure", Usage: providerapi.Usage{TotalTokens: 3}},
	}}
	broker := &recordingBroker{
		definitions: testDefinitions(),
		executeErr:  errors.New("process exited with code 1"),
	}
	request := testRunRequest()
	request.AllowedTools = []string{"test_read"}
	runner := newTestRunner(t, provider, broker, store)

	result, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "fixed after failure" || result.Iterations != 2 {
		t.Fatalf("result = %+v", result)
	}
	toolMsg := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if toolMsg.Role != providerapi.RoleTool || !strings.Contains(toolMsg.Content, "tool_failed") {
		t.Fatalf("tool message = %+v", toolMsg)
	}
}

func TestRunnerFeedsUnadvertisedToolAndContinues(t *testing.T) {
	store, _, _ := openAgentFixture(t)
	provider := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{
			ToolCalls: []providerapi.ToolCall{{ID: "call-ghost", Name: "http_get", Arguments: `{"url":"https://example.com"}`}},
			Usage:     providerapi.Usage{TotalTokens: 2},
		},
		{Content: "used advertised tools instead", Usage: providerapi.Usage{TotalTokens: 3}},
	}}
	broker := &recordingBroker{definitions: testDefinitions()}
	request := testRunRequest()
	request.AllowedTools = []string{"test_read"}
	runner := newTestRunner(t, provider, broker, store)

	result, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "used advertised tools instead" {
		t.Fatalf("result = %+v", result)
	}
	if broker.calls != 0 {
		t.Fatalf("broker calls = %d, want 0", broker.calls)
	}
	toolMsg := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if toolMsg.Role != providerapi.RoleTool || !strings.Contains(toolMsg.Content, "unadvertised_tool") {
		t.Fatalf("tool message = %+v", toolMsg)
	}
}

func TestRunnerFeedsDeniedToolResultAndContinues(t *testing.T) {
	store, _, _ := openAgentFixture(t)
	provider := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{
			ToolCalls: []providerapi.ToolCall{{ID: "call-deny", Name: "test_read", Arguments: `{"path":"rel"}`}},
			Usage:     providerapi.Usage{TotalTokens: 2},
		},
		{
			Content: "recovered after deny", Usage: providerapi.Usage{TotalTokens: 3},
		},
	}}
	broker := &recordingBroker{
		definitions: testDefinitions(),
		executeErr:  fmt.Errorf("%w: paths must be absolute", toolapi.ErrDenied),
	}
	request := testRunRequest()
	request.AllowedTools = []string{"test_read"}
	runner := newTestRunner(t, provider, broker, store)

	result, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "recovered after deny" || result.Iterations != 2 {
		t.Fatalf("result = %+v", result)
	}
	if broker.calls != 1 {
		t.Fatalf("tool calls = %d, want 1", broker.calls)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("provider requests = %d", len(provider.requests))
	}
	msgs := provider.requests[1].Messages
	if len(msgs) < 1 {
		t.Fatal("second provider request has no messages")
	}
	toolMsg := msgs[len(msgs)-1]
	if toolMsg.Role != providerapi.RoleTool || toolMsg.ToolCallID != "call-deny" {
		t.Fatalf("tool message = %+v", toolMsg)
	}
	if !strings.Contains(toolMsg.Content, "tool_denied") {
		t.Fatalf("tool content = %q, want tool_denied", toolMsg.Content)
	}
	if len(result.ToolCalls) != 1 || string(result.ToolCalls[0].Output) != toolMsg.Content {
		t.Fatalf("result tool calls = %+v", result.ToolCalls)
	}
}

func TestRunnerRestoresDeniedToolResultAfterRestart(t *testing.T) {
	store, _, _ := openAgentFixture(t)
	firstProvider := &sequenceProvider{
		responses: []providerapi.CompletionResponse{{
			ToolCalls: []providerapi.ToolCall{{ID: "call-deny", Name: "test_read", Arguments: `{}`}},
			Usage:     providerapi.Usage{TotalTokens: 2},
		}},
		errors: []error{nil, context.Canceled},
	}
	broker := &recordingBroker{
		definitions: testDefinitions(),
		executeErr:  fmt.Errorf("%w: duration exceeds grant", toolapi.ErrDenied),
	}
	request := testRunRequest()
	request.AllowedTools = []string{"test_read"}
	firstRunner := newTestRunner(t, firstProvider, broker, store)

	_, err := firstRunner.Run(context.Background(), request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("first Run() error = %v, want context.Canceled", err)
	}
	if broker.calls != 1 {
		t.Fatalf("tool calls before restart = %d, want 1", broker.calls)
	}

	secondProvider := &sequenceProvider{responses: []providerapi.CompletionResponse{{
		Content: "after deny restore", Usage: providerapi.Usage{TotalTokens: 3},
	}}}
	secondRunner := newTestRunner(t, secondProvider, broker, store)
	result, err := secondRunner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "after deny restore" || result.Iterations != 2 {
		t.Fatalf("recovered result = %+v", result)
	}
	if broker.calls != 1 {
		t.Fatalf("tool calls after restart = %d, want still 1", broker.calls)
	}
	if secondProvider.calls != 1 {
		t.Fatalf("second provider calls = %d, want 1", secondProvider.calls)
	}
	toolMsg := secondProvider.requests[0].Messages[len(secondProvider.requests[0].Messages)-1]
	if toolMsg.Role != providerapi.RoleTool || !strings.Contains(toolMsg.Content, "tool_denied") {
		t.Fatalf("restored tool message = %+v", toolMsg)
	}
}

func TestRunnerRespectsMaxIterations(t *testing.T) {
	store, db, _ := openAgentFixture(t)
	// Each response requests a tool call so the loop never finishes with content.
	var responses []providerapi.CompletionResponse
	for i := 0; i < 5; i++ {
		responses = append(responses, providerapi.CompletionResponse{
			ToolCalls: []providerapi.ToolCall{{
				ID: fmt.Sprintf("call-%d", i), Name: "test_read", Arguments: `{}`,
			}},
			Usage: providerapi.Usage{TotalTokens: 1, InputTokens: 1},
		})
	}
	provider := &sequenceProvider{responses: responses}
	broker := &recordingBroker{db: db, definitions: testDefinitions()}
	runner, err := New(Config{
		Provider: provider, Broker: broker, Records: store, Model: "test-model",
		MaxIterations: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := testRunRequest()
	request.AllowedTools = []string{"test_read"}
	result, err := runner.Run(context.Background(), request)
	// Soft landing: final iteration advertises no tools and returns a text stop message
	// instead of hard-failing when the model only emits tool calls.
	if err != nil {
		t.Fatalf("Run() error = %v, want nil (soft landing)", err)
	}
	if result.Iterations != 2 {
		t.Fatalf("iterations = %d, want 2", result.Iterations)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	if strings.TrimSpace(result.Content) == "" {
		t.Fatal("expected soft-landing content")
	}
}

func TestRunnerDefaultHasNoStepCap(t *testing.T) {
	store, db, _ := openAgentFixture(t)
	var responses []providerapi.CompletionResponse
	for i := 0; i < 20; i++ {
		responses = append(responses, providerapi.CompletionResponse{
			ToolCalls: []providerapi.ToolCall{{
				ID: fmt.Sprintf("call-uncap-%d", i), Name: "test_read",
				Arguments: fmt.Sprintf(`{"n":%d}`, i),
			}},
			Usage: providerapi.Usage{TotalTokens: 1, InputTokens: 1},
		})
	}
	responses = append(responses, providerapi.CompletionResponse{Content: "done after 20 tools", Usage: providerapi.Usage{TotalTokens: 1}})
	provider := &sequenceProvider{responses: responses}
	broker := &recordingBroker{db: db, definitions: testDefinitions()}
	runner := newTestRunner(t, provider, broker, store)
	request := testRunRequest()
	request.AllowedTools = []string{"test_read"}
	result, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Iterations != 21 || result.Content != "done after 20 tools" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunnerResumePromptContinuesAfterFinalAssistant(t *testing.T) {
	store, _, _ := openAgentFixture(t)
	provider := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{Content: "first", Usage: providerapi.Usage{TotalTokens: 1}},
		{Content: "second", Usage: providerapi.Usage{TotalTokens: 1}},
	}}
	runner := newTestRunner(t, provider, &recordingBroker{definitions: testDefinitions()}, store)
	request := testRunRequest()
	first, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Content != "first" {
		t.Fatalf("first = %+v", first)
	}
	prefix, err := store.InitialPrefix(context.Background(), request.RunID)
	if err != nil {
		t.Fatal(err)
	}
	request.Messages = prefix
	request.ResumePrompt = "continue"
	second, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != "second" {
		t.Fatalf("second = %+v", second)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d", provider.calls)
	}
	tail := provider.requests[1].Messages
	if tail[len(tail)-1].Role != providerapi.RoleUser || tail[len(tail)-1].Content != "continue" {
		t.Fatalf("resume tail = %+v", tail[len(tail)-1])
	}
}

func TestRunnerResumePromptContinuesAfterIncompleteHistory(t *testing.T) {
	store, db, _ := openAgentFixture(t)
	firstProvider := &sequenceProvider{
		responses: []providerapi.CompletionResponse{{
			ToolCalls: []providerapi.ToolCall{{ID: "call-resume", Name: "test_read", Arguments: `{}`}},
			Usage:     providerapi.Usage{TotalTokens: 2},
		}},
		errors: []error{nil, context.Canceled},
	}
	broker := &recordingBroker{db: db, definitions: testDefinitions()}
	request := testRunRequest()
	request.AllowedTools = []string{"test_read"}
	first := newTestRunner(t, firstProvider, broker, store)
	_, err := first.Run(context.Background(), request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("first Run() error = %v, want context.Canceled", err)
	}
	prefix, err := store.InitialPrefix(context.Background(), request.RunID)
	if err != nil {
		t.Fatal(err)
	}
	request.Messages = prefix
	request.ResumePrompt = "continue after tools"
	secondProvider := &sequenceProvider{responses: []providerapi.CompletionResponse{{
		Content: "second", Usage: providerapi.Usage{TotalTokens: 1},
	}}}
	second := newTestRunner(t, secondProvider, broker, store)
	got, err := second.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "second" {
		t.Fatalf("second = %+v", got)
	}
	tail := secondProvider.requests[0].Messages
	if tail[len(tail)-1].Role != providerapi.RoleUser || tail[len(tail)-1].Content != "continue after tools" {
		t.Fatalf("resume tail = %+v", tail[len(tail)-1])
	}
}

func TestRunnerClaimsNextStepInboxAfterText(t *testing.T) {
	store, _, _ := openAgentFixture(t)
	provider := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{Content: "first answer", Usage: providerapi.Usage{TotalTokens: 1}},
		{Content: "after steer", Usage: providerapi.Usage{TotalTokens: 1}},
	}}
	runner := newTestRunner(t, provider, &recordingBroker{definitions: testDefinitions()}, store)
	request := testRunRequest()
	request.SessionID = "session-steer"
	request.AllowedTools = []string{"test_read"}
	runner.Inbox().Steer(request.SessionID, request.RunID, "steer-1", "do this instead")
	result, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "after steer" || result.Iterations != 2 {
		t.Fatalf("result = %+v", result)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	second := provider.requests[1].Messages
	if second[len(second)-1].Role != providerapi.RoleUser || second[len(second)-1].Content != "do this instead" {
		t.Fatalf("second request tail = %+v", second[len(second)-1])
	}
	if runner.Inbox().Pending(request.SessionID) {
		t.Fatal("next-step inbox should be empty after claim")
	}
}

func TestRunnerStillFailsOnCanceledParentContext(t *testing.T) {
	store, _, _ := openAgentFixture(t)
	provider := &sequenceProvider{responses: []providerapi.CompletionResponse{{
		ToolCalls: []providerapi.ToolCall{{ID: "call-cancel", Name: "test_read", Arguments: `{}`}},
		Usage:     providerapi.Usage{TotalTokens: 2},
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	broker := &recordingBroker{definitions: testDefinitions(), executeErr: context.Canceled, onExecute: cancel}
	request := testRunRequest()
	request.AllowedTools = []string{"test_read"}
	runner := newTestRunner(t, provider, broker, store)

	_, err := runner.Run(ctx, request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
}

func TestRunnerFeedsCanceledToolWhenParentStillAlive(t *testing.T) {
	store, _, _ := openAgentFixture(t)
	provider := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{
			ToolCalls: []providerapi.ToolCall{{ID: "call-timeout", Name: "test_read", Arguments: `{}`}},
			Usage:     providerapi.Usage{TotalTokens: 2},
		},
		{Content: "continued after tool cancel", Usage: providerapi.Usage{TotalTokens: 3}},
	}}
	broker := &recordingBroker{definitions: testDefinitions(), executeErr: context.DeadlineExceeded}
	request := testRunRequest()
	request.AllowedTools = []string{"test_read"}
	runner := newTestRunner(t, provider, broker, store)

	result, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "continued after tool cancel" || result.Iterations != 2 {
		t.Fatalf("result = %+v", result)
	}
	toolMsg := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if toolMsg.Role != providerapi.RoleTool || !strings.Contains(toolMsg.Content, "tool_failed") {
		t.Fatalf("tool message = %+v", toolMsg)
	}
}

func TestRunnerRestoresDurableToolTurnAfterRestart(t *testing.T) {
	store, db, _ := openAgentFixture(t)
	firstProvider := &sequenceProvider{
		responses: []providerapi.CompletionResponse{{
			ToolCalls: []providerapi.ToolCall{{ID: "call-1", Name: "test_read", Arguments: `{}`}},
			Usage:     providerapi.Usage{TotalTokens: 4},
		}},
		errors: []error{nil, context.Canceled},
	}
	broker := &recordingBroker{db: db, definitions: testDefinitions()}
	request := testRunRequest()
	request.AllowedTools = []string{"test_read"}
	request.MaxTotalTokens = 20
	firstRunner := newTestRunner(t, firstProvider, broker, store)

	_, err := firstRunner.Run(context.Background(), request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("first Run() error = %v, want context.Canceled", err)
	}
	if broker.calls != 1 {
		t.Fatalf("tool calls before restart = %d, want 1", broker.calls)
	}

	secondProvider := &sequenceProvider{responses: []providerapi.CompletionResponse{{
		Content: "recovered", Usage: providerapi.Usage{TotalTokens: 3},
	}}}
	secondRunner := newTestRunner(t, secondProvider, broker, store)
	result, err := secondRunner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "recovered" || result.Iterations != 2 || result.Usage.TotalTokens != 7 {
		t.Fatalf("recovered result = %+v", result)
	}
	if broker.calls != 1 {
		t.Fatalf("tool calls after restart = %d, want still 1", broker.calls)
	}
	if secondProvider.calls != 1 || len(secondProvider.requests[0].Messages) != 4 {
		t.Fatalf("second provider calls/messages = %d/%d", secondProvider.calls, len(secondProvider.requests[0].Messages))
	}
	if got := secondProvider.requests[0].Messages[3]; got.Role != providerapi.RoleTool || got.ToolCallID != "call-1" {
		t.Fatalf("restored tool message = %+v", got)
	}
}

type sequenceProvider struct {
	responses []providerapi.CompletionResponse
	errors    []error
	requests  []providerapi.CompletionRequest
	calls     int
}

func (p *sequenceProvider) Stream(_ context.Context, request providerapi.CompletionRequest, handler providerapi.StreamHandler) error {
	index := p.calls
	p.calls++
	p.requests = append(p.requests, request)
	if index < len(p.errors) && p.errors[index] != nil {
		return p.errors[index]
	}
	if index >= len(p.responses) {
		return errors.New("unexpected provider call")
	}
	return providerapi.EmitResponse(p.responses[index], handler)
}

type longOutputBroker struct {
	recordingBroker
	output string
}

func (b *longOutputBroker) Execute(_ context.Context, request toolapi.Request) (toolapi.Response, error) {
	b.calls++
	if b.executeErr != nil {
		return toolapi.Response{}, b.executeErr
	}
	now := time.Now().UTC()
	response := toolapi.Response{
		CallID: request.CallID, Tool: request.Tool, Output: json.RawMessage(b.output),
		StartedAt: now, FinishedAt: now,
	}
	if b.db != nil {
		payload, err := json.Marshal(map[string]any{"response": response})
		if err != nil {
			return toolapi.Response{}, err
		}
		_, err = b.db.Exec(`INSERT INTO tool_calls
			(tool_call_id, run_id, step_id, grant_id, tool_name, state, request, response, started_at, finished_at)
			VALUES (?, ?, ?, NULL, ?, 'succeeded', ?, ?, ?, ?)`,
			request.CallID, request.RunID, request.StepID, request.Tool, string(request.Arguments), string(payload),
			now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
		)
		if err != nil {
			return toolapi.Response{}, err
		}
	}
	return response, nil
}

type recordingBroker struct {
	db          *sql.DB
	definitions []toolapi.Definition
	calls       int
	executeErr  error
	onExecute   func()
}

func (b *recordingBroker) Definitions() []toolapi.Definition {
	return append([]toolapi.Definition(nil), b.definitions...)
}

func (b *recordingBroker) Execute(_ context.Context, request toolapi.Request) (toolapi.Response, error) {
	b.calls++
	if b.onExecute != nil {
		b.onExecute()
	}
	if b.executeErr != nil {
		return toolapi.Response{}, b.executeErr
	}
	now := time.Now().UTC()
	response := toolapi.Response{
		CallID: request.CallID, Tool: request.Tool, Output: json.RawMessage(`{"ok":true}`),
		StartedAt: now, FinishedAt: now,
	}
	if b.db != nil {
		payload, err := json.Marshal(map[string]any{"response": response})
		if err != nil {
			return toolapi.Response{}, err
		}
		_, err = b.db.Exec(`INSERT INTO tool_calls
			(tool_call_id, run_id, step_id, grant_id, tool_name, state, request, response, started_at, finished_at)
			VALUES (?, ?, ?, NULL, ?, 'succeeded', ?, ?, ?, ?)`,
			request.CallID, request.RunID, request.StepID, request.Tool, string(request.Arguments), string(payload),
			now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
		)
		if err != nil {
			return toolapi.Response{}, err
		}
	}
	return response, nil
}

func newTestRunner(t *testing.T, provider StreamingProvider, broker ToolBroker, store *RecordStore) *Runner {
	t.Helper()
	runner, err := New(Config{Provider: provider, Broker: broker, Records: store, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func TestPackForProviderDoesNotDropPrefixOrCurrentUser(t *testing.T) {
	store, _, _ := openAgentFixture(t)
	runner := newTestRunner(t, &sequenceProvider{}, &recordingBroker{}, store)
	prefix := "AGENTS-keep-" + strings.Repeat("P", 200)
	current := "current-user-keep"
	msgs := []providerapi.Message{
		{Role: providerapi.RoleSystem, Content: prefix},
		{Role: providerapi.RoleUser, Content: "old"},
		{Role: providerapi.RoleAssistant, Content: strings.Repeat("A", 4_000)},
		{Role: providerapi.RoleUser, Content: current},
	}
	packed, _, _, _ := runner.packForProvider(msgs, nil, RunRequest{ContextWindow: 2_000, MaxOutputTokens: 256}, "test-model", 256)
	if len(packed) == 0 || packed[0].Content != prefix {
		t.Fatalf("prefix dropped: %+v", packed)
	}
	if packed[len(packed)-1].Content != current {
		t.Fatalf("current user dropped: %+v", packed[len(packed)-1])
	}
}

func TestRebuildProviderViewKeepsTodoEphemeral(t *testing.T) {
	store, _, _ := openAgentFixture(t)
	runner := newTestRunner(t, &sequenceProvider{}, &recordingBroker{}, store)
	todo := contextpack.TodoSystemPrefix + "\n- [in_progress] patch fs.go"
	msgs := []providerapi.Message{
		{Role: providerapi.RoleSystem, Content: "sys"},
		{Role: providerapi.RoleUser, Content: "old1"},
		{Role: providerapi.RoleAssistant, Content: "a1"},
		{Role: providerapi.RoleUser, Content: "old2"},
		{Role: providerapi.RoleAssistant, Content: "a2"},
		{Role: providerapi.RoleUser, Content: "old3"},
		{Role: providerapi.RoleAssistant, Content: "a3"},
		{Role: providerapi.RoleSystem, Content: todo},
		{Role: providerapi.RoleUser, Content: "now"},
	}
	out := runner.rebuildProviderView(context.Background(), msgs, RunRequest{ContextWindow: 128_000}, "test-model")
	found := false
	for _, m := range out {
		if contextpack.IsTodoSystem(m) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("todo block missing after rebuild: %+v", out)
	}
}

func testRunRequest() RunRequest {
	return RunRequest{
		RunID: "run-agent", TaskID: "task-agent", PlanID: "plan-agent", PlanHash: "hash-agent",
		StepID: "step-agent", Actor: "agent", TraceID: "trace-agent",
		Messages: []providerapi.Message{
			{Role: providerapi.RoleSystem, Content: "Follow the plan."},
			{Role: providerapi.RoleUser, Content: "Inspect progress."},
		},
	}
}

func TestSnapshotForRoleFallsBackAndOverrides(t *testing.T) {
	mainP := &sequenceProvider{}
	subP := &sequenceProvider{}
	compactP := &sequenceProvider{}
	store, _, _ := openAgentFixture(t)
	runner, err := New(Config{
		Provider: mainP, Broker: &recordingBroker{}, Records: store, Model: "main-model",
		ContextWindow: 8000,
		Roles: map[string]RoleEndpoint{
			"subagent": {Provider: subP, Model: "sub-model", ContextWindow: 4000},
			"compact":  {Provider: compactP, Model: "compact-model", ContextWindow: 2000},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	p, m, cw := runner.snapshotForRole("")
	if p != mainP || m != "main-model" || cw != 8000 {
		t.Fatalf("main empty role: %v %q %d", p, m, cw)
	}
	p, m, cw = runner.snapshotForRole("main")
	if p != mainP || m != "main-model" || cw != 8000 {
		t.Fatalf("main named: %v %q %d", p, m, cw)
	}
	p, m, cw = runner.snapshotForRole("subagent")
	if p != subP || m != "sub-model" || cw != 4000 {
		t.Fatalf("subagent: %v %q %d", p, m, cw)
	}
	p, m, cw = runner.snapshotForRole("compact")
	if p != compactP || m != "compact-model" || cw != 2000 {
		t.Fatalf("compact: %v %q %d", p, m, cw)
	}
	p, m, cw = runner.snapshotForRole("unknown")
	if p != mainP || m != "main-model" || cw != 8000 {
		t.Fatalf("unknown role should fall back: %v %q %d", p, m, cw)
	}
}

func TestCompactSummaryUsesCompactRole(t *testing.T) {
	mainP := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{Content: "main-summary"},
	}}
	compactP := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{Content: "cheap-summary"},
	}}
	store, _, _ := openAgentFixture(t)
	runner, err := New(Config{
		Provider: mainP, Broker: &recordingBroker{}, Records: store, Model: "main-model",
		Roles: map[string]RoleEndpoint{
			"compact": {Provider: compactP, Model: "cheap-model"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sum, err := runner.CompactSummary(context.Background(), []providerapi.Message{
		{Role: providerapi.RoleUser, Content: "hello world for compaction"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum != "cheap-summary" {
		t.Fatalf("summary = %q", sum)
	}
	if compactP.calls != 1 || mainP.calls != 0 {
		t.Fatalf("compact calls=%d main calls=%d", compactP.calls, mainP.calls)
	}
	if len(compactP.requests) != 1 || compactP.requests[0].Model != "cheap-model" {
		t.Fatalf("compact request model = %v", compactP.requests)
	}
}

func TestRunUsesModelOverride(t *testing.T) {
	mainP := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{Content: "main-reply"},
	}}
	prefP := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{Content: "prefer-reply"},
	}}
	store, _, _ := openAgentFixture(t)
	runner, err := New(Config{
		Provider: mainP, Broker: &recordingBroker{}, Records: store, Model: "main-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := testRunRequest()
	req.ModelOverride = "prefer-model"
	req.OverrideProvider = prefP
	req.OverrideContextWindow = 4096
	result, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "prefer-reply" {
		t.Fatalf("content = %q", result.Content)
	}
	if prefP.calls != 1 || mainP.calls != 0 {
		t.Fatalf("prefer calls=%d main calls=%d", prefP.calls, mainP.calls)
	}
	if len(prefP.requests) != 1 || prefP.requests[0].Model != "prefer-model" {
		t.Fatalf("prefer request model = %v", prefP.requests)
	}
}

func TestRunModelOverrideDoesNotBeatSubagentRole(t *testing.T) {
	mainP := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{Content: "main-reply"},
	}}
	subP := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{Content: "sub-reply"},
	}}
	prefP := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{Content: "prefer-reply"},
	}}
	store, _, _ := openAgentFixture(t)
	runner, err := New(Config{
		Provider: mainP, Broker: &recordingBroker{}, Records: store, Model: "main-model",
		Roles: map[string]RoleEndpoint{
			"subagent": {Provider: subP, Model: "sub-model"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := testRunRequest()
	req.Role = "subagent"
	req.ModelOverride = "prefer-model"
	req.OverrideProvider = prefP
	result, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "sub-reply" {
		t.Fatalf("content = %q", result.Content)
	}
	if subP.calls != 1 || prefP.calls != 0 {
		t.Fatalf("sub=%d prefer=%d", subP.calls, prefP.calls)
	}
}

func TestRunUsesSubagentRole(t *testing.T) {
	mainP := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{Content: "main-reply"},
	}}
	subP := &sequenceProvider{responses: []providerapi.CompletionResponse{
		{Content: "sub-reply"},
	}}
	store, _, _ := openAgentFixture(t)
	runner, err := New(Config{
		Provider: mainP, Broker: &recordingBroker{}, Records: store, Model: "main-model",
		Roles: map[string]RoleEndpoint{
			"subagent": {Provider: subP, Model: "sub-model"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := testRunRequest()
	req.Role = "subagent"
	result, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "sub-reply" {
		t.Fatalf("content = %q", result.Content)
	}
	if subP.calls != 1 || mainP.calls != 0 {
		t.Fatalf("sub calls=%d main calls=%d", subP.calls, mainP.calls)
	}
	if len(subP.requests) != 1 || subP.requests[0].Model != "sub-model" {
		t.Fatalf("sub models = %v", subP.requests)
	}
}

func testDefinitions() []toolapi.Definition {
	return []toolapi.Definition{{
		Name: "test_read", Description: "Read test state", InputSchema: json.RawMessage(`{"type":"object"}`),
	}}
}

func openAgentFixture(t *testing.T) (*RecordStore, *sql.DB, func()) {
	t.Helper()
	database, err := coresqlite.Open(context.Background(), filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	closeDB := func() { _ = database.Close() }
	t.Cleanup(closeDB)
	db := database.SQL()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	statements := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO sessions(session_id,state,created_at,updated_at) VALUES(?,?,?,?)", []any{"session-agent", "active", stamp, stamp}},
		{"INSERT INTO tasks(task_id,session_id,title,objective,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", []any{"task-agent", "session-agent", "Agent", "Inspect progress", "running", stamp, stamp}},
		{"INSERT INTO plans(plan_id,task_id,revision,state,scope_hash,created_at,updated_at,document) VALUES(?,?,?,?,?,?,?,?)", []any{"plan-agent", "task-agent", 1, "approved", "hash-agent", stamp, stamp, `{}`}},
		{"INSERT INTO plan_steps(step_id,plan_id,position,title,state,effect_level,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", []any{"step-agent", "plan-agent", 0, "Inspect", "running", "R0", stamp, stamp}},
		{"INSERT INTO runs(run_id,task_id,plan_id,state,started_at,updated_at,step_id) VALUES(?,?,?,?,?,?,?)", []any{"run-agent", "task-agent", "plan-agent", "running", stamp, stamp, "step-agent"}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			closeDB()
			t.Fatal(err)
		}
	}
	store, err := NewRecordStore(db)
	if err != nil {
		closeDB()
		t.Fatal(err)
	}
	return store, db, closeDB
}

func TestProjectToolResultIncludesNameAndPath(t *testing.T) {
	got := projectToolResult("fs_read", providerapi.Message{
		Content: `{"path":"/tmp/ws/a.go","content":"package main"}`,
	})
	if !strings.Contains(got, "fs_read") || !strings.Contains(got, "/tmp/ws/a.go") {
		t.Fatalf("got %q", got)
	}
}
