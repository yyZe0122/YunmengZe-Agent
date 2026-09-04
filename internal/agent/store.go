package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

var (
	ErrCorruptHistory   = errors.New("corrupt agent run history")
	ErrRecoveryConflict = errors.New("agent recovery input conflicts with persisted history")
	ErrRecoveryBlocked  = errors.New("agent recovery requires a new approval or run")
)

type RecordType string

const (
	RecordInputMessage     RecordType = "input_message"
	RecordAssistantMessage RecordType = "assistant_message"
	RecordToolResult       RecordType = "tool_result"
)

type RunRecord struct {
	RunID        string
	Position     int
	Type         RecordType
	Message      providerapi.Message
	Usage        providerapi.Usage
	FinishReason string
	ToolCallID   string
	CreatedAt    time.Time
}

// RecordStore is the append-only source for provider-neutral Agent messages.
// Tool execution remains authoritative in tool_calls; approvals and grants
// remain authoritative in their own Core tables.
type RecordStore struct {
	db  *sql.DB
	now func() time.Time
}

type storedToolCall struct {
	state    string
	response sql.NullString
}

func NewRecordStore(db *sql.DB) (*RecordStore, error) {
	if db == nil {
		return nil, errors.New("agent record database is required")
	}
	return &RecordStore{db: db, now: func() time.Time { return time.Now().UTC() }}, nil
}

// Prepare atomically writes the initial request messages for a new run. On
// recovery it verifies that the caller supplied the exact same immutable
// prefix and returns the full ordered history.
func (s *RecordStore) Prepare(ctx context.Context, runID string, initial []providerapi.Message) ([]RunRecord, error) {
	if ctx == nil {
		return nil, errors.New("agent record context is required")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" || len(initial) == 0 {
		return nil, fmt.Errorf("%w: run ID and initial messages are required", ErrRecoveryConflict)
	}
	for _, message := range initial {
		if err := validateStoredMessage(message); err != nil {
			return nil, fmt.Errorf("%w: invalid initial message: %v", ErrRecoveryConflict, err)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin agent record preparation: %w", err)
	}
	defer tx.Rollback()

	records, err := listRunRecords(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		createdAt := s.now().UTC()
		for position, message := range initial {
			if err := insertRunRecord(ctx, tx, RunRecord{
				RunID: runID, Position: position, Type: RecordInputMessage,
				Message: cloneMessage(message), CreatedAt: createdAt,
			}); err != nil {
				return nil, err
			}
		}
		records, err = listRunRecords(ctx, tx, runID)
		if err != nil {
			return nil, err
		}
	}
	if err := verifyInitialPrefix(records, initial); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit agent record preparation: %w", err)
	}
	return records, nil
}

func (s *RecordStore) AppendAssistant(
	ctx context.Context,
	runID string,
	message providerapi.Message,
	usage providerapi.Usage,
	finishReason string,
) (RunRecord, error) {
	if message.Role != providerapi.RoleAssistant {
		return RunRecord{}, fmt.Errorf("%w: assistant record must use assistant role", ErrCorruptHistory)
	}
	return s.append(ctx, RunRecord{
		RunID: strings.TrimSpace(runID), Type: RecordAssistantMessage,
		Message: cloneMessage(message), Usage: usage, FinishReason: strings.TrimSpace(finishReason),
	})
}

func (s *RecordStore) AppendUser(ctx context.Context, runID string, message providerapi.Message) (RunRecord, error) {
	if message.Role != providerapi.RoleUser {
		return RunRecord{}, fmt.Errorf("%w: user record must use user role", ErrCorruptHistory)
	}
	if strings.TrimSpace(message.Content) == "" {
		return RunRecord{}, fmt.Errorf("%w: user record requires content", ErrCorruptHistory)
	}
	return s.append(ctx, RunRecord{
		RunID: strings.TrimSpace(runID), Type: RecordInputMessage,
		Message: cloneMessage(message),
	})
}

func (s *RecordStore) AppendToolResult(ctx context.Context, runID string, message providerapi.Message) (RunRecord, error) {
	if message.Role != providerapi.RoleTool || strings.TrimSpace(message.ToolCallID) == "" {
		return RunRecord{}, fmt.Errorf("%w: tool result requires tool role and call ID", ErrCorruptHistory)
	}
	return s.append(ctx, RunRecord{
		RunID: strings.TrimSpace(runID), Type: RecordToolResult,
		Message: cloneMessage(message), ToolCallID: strings.TrimSpace(message.ToolCallID),
	})
}

func (s *RecordStore) List(ctx context.Context, runID string) ([]RunRecord, error) {
	if ctx == nil {
		return nil, errors.New("agent record context is required")
	}
	return listRunRecords(ctx, s.db, strings.TrimSpace(runID))
}

// InitialPrefix returns the leading input_message records used as Prepare's
// immutable prefix (ADR-030). Resume must pass this back, not a rebuilt prompt.
func (s *RecordStore) InitialPrefix(ctx context.Context, runID string) ([]providerapi.Message, error) {
	records, err := s.List(ctx, runID)
	if err != nil {
		return nil, err
	}
	return initialPrefixMessages(records), nil
}

func initialPrefixMessages(records []RunRecord) []providerapi.Message {
	out := make([]providerapi.Message, 0, 4)
	for _, record := range records {
		if record.Type != RecordInputMessage {
			break
		}
		out = append(out, cloneMessage(record.Message))
	}
	return out
}

func (s *RecordStore) LoadSucceededToolCall(ctx context.Context, runID, callID string) (toolapi.Response, error) {
	if ctx == nil {
		return toolapi.Response{}, errors.New("agent record context is required")
	}
	var stored storedToolCall
	err := s.db.QueryRowContext(ctx, `
		SELECT state, response FROM tool_calls WHERE run_id = ? AND tool_call_id = ?`,
		strings.TrimSpace(runID), strings.TrimSpace(callID),
	).Scan(&stored.state, &stored.response)
	if errors.Is(err, sql.ErrNoRows) {
		return toolapi.Response{}, fmt.Errorf("%w: tool call %s has no durable execution", ErrRecoveryBlocked, callID)
	}
	if err != nil {
		return toolapi.Response{}, fmt.Errorf("load tool call %s: %w", callID, err)
	}
	return decodeSucceededToolCall(callID, stored)
}

func (s *RecordStore) loadToolCalls(ctx context.Context, runID string) (map[string]storedToolCall, error) {
	if ctx == nil {
		return nil, errors.New("agent record context is required")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT tool_call_id, state, response FROM tool_calls WHERE run_id = ?`,
		strings.TrimSpace(runID),
	)
	if err != nil {
		return nil, fmt.Errorf("load tool calls for run %s: %w", runID, err)
	}
	defer rows.Close()

	toolCalls := make(map[string]storedToolCall)
	for rows.Next() {
		var callID string
		var stored storedToolCall
		if err := rows.Scan(&callID, &stored.state, &stored.response); err != nil {
			return nil, fmt.Errorf("load tool calls for run %s: %w", runID, err)
		}
		toolCalls[callID] = stored
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load tool calls for run %s: %w", runID, err)
	}
	return toolCalls, nil
}

func decodeSucceededToolCall(callID string, stored storedToolCall) (toolapi.Response, error) {
	if stored.state != "succeeded" || !stored.response.Valid {
		return toolapi.Response{}, fmt.Errorf("%w: tool call %s is %s", ErrRecoveryBlocked, callID, stored.state)
	}
	var payload struct {
		Response toolapi.Response `json:"response"`
		Error    string           `json:"error,omitempty"`
	}
	if err := json.Unmarshal([]byte(stored.response.String), &payload); err != nil {
		return toolapi.Response{}, fmt.Errorf("%w: decode tool call %s: %v", ErrCorruptHistory, callID, err)
	}
	if payload.Response.CallID != callID || strings.TrimSpace(payload.Response.Tool) == "" {
		return toolapi.Response{}, fmt.Errorf("%w: invalid persisted response for tool call %s", ErrCorruptHistory, callID)
	}
	return payload.Response, nil
}

func (s *RecordStore) append(ctx context.Context, record RunRecord) (RunRecord, error) {
	if ctx == nil {
		return RunRecord{}, errors.New("agent record context is required")
	}
	if record.RunID == "" {
		return RunRecord{}, fmt.Errorf("%w: run ID is required", ErrCorruptHistory)
	}
	if err := validateStoredMessage(record.Message); err != nil {
		return RunRecord{}, fmt.Errorf("%w: %v", ErrCorruptHistory, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RunRecord{}, fmt.Errorf("begin agent record append: %w", err)
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(position), -1) + 1 FROM agent_run_records WHERE run_id = ?", record.RunID,
	).Scan(&record.Position); err != nil {
		return RunRecord{}, fmt.Errorf("select next agent record position: %w", err)
	}
	record.CreatedAt = s.now().UTC()
	if err := insertRunRecord(ctx, tx, record); err != nil {
		return RunRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return RunRecord{}, fmt.Errorf("commit agent record append: %w", err)
	}
	return record, nil
}

type recordQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type recordExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func listRunRecords(ctx context.Context, queryer recordQueryer, runID string) ([]RunRecord, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT position, record_type, message, usage, finish_reason, tool_call_id, created_at
		FROM agent_run_records WHERE run_id = ? ORDER BY position`, runID)
	if err != nil {
		return nil, fmt.Errorf("list agent run records: %w", err)
	}
	defer rows.Close()

	var records []RunRecord
	// RawBytes reuses the Rows scan buffer, so decode both values before the next Rows.Next call.
	var messageJSON, usageJSON sql.RawBytes
	var createdAt string
	for rows.Next() {
		records = append(records, RunRecord{RunID: runID})
		record := &records[len(records)-1]
		if err := rows.Scan(
			&record.Position, &record.Type, &messageJSON, &usageJSON,
			&record.FinishReason, &record.ToolCallID, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent run record: %w", err)
		}
		if record.Position != len(records)-1 {
			return nil, fmt.Errorf("%w: run %s has non-contiguous position %d", ErrCorruptHistory, runID, record.Position)
		}
		if err := json.Unmarshal(messageJSON, &record.Message); err != nil {
			return nil, fmt.Errorf("%w: decode message at position %d: %v", ErrCorruptHistory, record.Position, err)
		}
		if err := json.Unmarshal(usageJSON, &record.Usage); err != nil {
			return nil, fmt.Errorf("%w: decode usage at position %d: %v", ErrCorruptHistory, record.Position, err)
		}
		if err := validateRecord(*record); err != nil {
			return nil, err
		}
		parsedAt, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("%w: parse record time at position %d: %v", ErrCorruptHistory, record.Position, err)
		}
		record.CreatedAt = parsedAt.UTC()
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent run records: %w", err)
	}
	return records, nil
}

func insertRunRecord(ctx context.Context, execer recordExecer, record RunRecord) error {
	messageJSON, err := json.Marshal(record.Message)
	if err != nil {
		return fmt.Errorf("marshal agent message: %w", err)
	}
	usageJSON, err := json.Marshal(record.Usage)
	if err != nil {
		return fmt.Errorf("marshal agent usage: %w", err)
	}
	_, err = execer.ExecContext(ctx, `
		INSERT INTO agent_run_records (
			run_id, position, record_type, message, usage, finish_reason, tool_call_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		record.RunID, record.Position, record.Type, string(messageJSON), string(usageJSON),
		record.FinishReason, record.ToolCallID, record.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert agent run record: %w", err)
	}
	return nil
}

func verifyInitialPrefix(records []RunRecord, initial []providerapi.Message) error {
	if len(records) < len(initial) {
		return fmt.Errorf("%w: persisted record count %d is shorter than request prefix %d", ErrRecoveryConflict, len(records), len(initial))
	}
	for i := range initial {
		if records[i].Type != RecordInputMessage {
			return fmt.Errorf("%w: persisted prefix record %d is %s, want input_message", ErrRecoveryConflict, i, records[i].Type)
		}
		if !messagesEqual(records[i].Message, initial[i]) {
			return fmt.Errorf("%w: input message %d differs", ErrRecoveryConflict, i)
		}
	}
	return nil
}

func validateRecord(record RunRecord) error {
	if err := validateStoredMessage(record.Message); err != nil {
		return fmt.Errorf("%w: position %d: %v", ErrCorruptHistory, record.Position, err)
	}
	switch record.Type {
	case RecordInputMessage:
		if record.Position < 0 || record.ToolCallID != "" {
			return fmt.Errorf("%w: invalid input record at position %d", ErrCorruptHistory, record.Position)
		}
	case RecordAssistantMessage:
		if record.Message.Role != providerapi.RoleAssistant || record.ToolCallID != "" {
			return fmt.Errorf("%w: invalid assistant record at position %d", ErrCorruptHistory, record.Position)
		}
	case RecordToolResult:
		if record.Message.Role != providerapi.RoleTool || record.ToolCallID == "" || record.Message.ToolCallID != record.ToolCallID {
			return fmt.Errorf("%w: invalid tool result record at position %d", ErrCorruptHistory, record.Position)
		}
	default:
		return fmt.Errorf("%w: unknown record type %q", ErrCorruptHistory, record.Type)
	}
	return nil
}

func validateStoredMessage(message providerapi.Message) error {
	switch message.Role {
	case providerapi.RoleSystem, providerapi.RoleUser, providerapi.RoleAssistant, providerapi.RoleTool:
	default:
		return fmt.Errorf("unknown message role %q", message.Role)
	}
	if message.Role == providerapi.RoleTool {
		if strings.TrimSpace(message.ToolCallID) == "" || len(message.ToolCalls) != 0 {
			return errors.New("tool message requires call ID and cannot contain tool calls")
		}
	} else if message.ToolCallID != "" {
		return errors.New("only tool messages may contain a tool call ID")
	}
	if len(message.ToolCalls) != 0 && message.Role != providerapi.RoleAssistant {
		return errors.New("only assistant messages may contain tool calls")
	}
	for _, call := range message.ToolCalls {
		if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" || !json.Valid([]byte(call.Arguments)) {
			return errors.New("message contains an invalid tool call")
		}
	}
	return nil
}

func messagesEqual(left, right providerapi.Message) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func cloneMessage(message providerapi.Message) providerapi.Message {
	message.ToolCalls = append([]providerapi.ToolCall(nil), message.ToolCalls...)
	return message
}
