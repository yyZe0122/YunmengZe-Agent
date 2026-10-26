// Package mcp implements on-demand stdio and remote MCP clients.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

const maxRPCMessageBytes = 8 << 20

// ToolDesc is a tool advertised by tools/list.
type ToolDesc struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Session is one connected MCP server (stdio or remote).
type Session interface {
	ListTools(ctx context.Context) ([]ToolDesc, error)
	CallTool(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error)
	Close() error
}

// parseToolList decodes a tools/list result into ToolDesc values.
func parseToolList(result struct {
	Tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	} `json:"tools"`
}) []ToolDesc {
	out := make([]ToolDesc, 0, len(result.Tools))
	for _, tool := range result.Tools {
		schema := tool.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, ToolDesc{
			Name: tool.Name, Description: tool.Description, InputSchema: schema,
		})
	}
	return out
}

func idEqual(raw any, want int64) bool {
	switch v := raw.(type) {
	case float64:
		return v == float64(want)
	case json.Number:
		n, err := v.Int64()
		return err == nil && n == want
	case int64:
		return v == want
	case int:
		return int64(v) == want
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		return err == nil && n == want
	default:
		return false
	}
}

func readRPCBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxRPCMessageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxRPCMessageBytes {
		return nil, errors.New("mcp response exceeds 8 MiB")
	}
	return body, nil
}

func decodeRPCResult(server, method string, msg map[string]any, result any) error {
	value, hasResult := msg["result"]
	errObj, hasError := msg["error"]
	if hasResult == hasError || hasError && errObj == nil {
		return fmt.Errorf("mcp %s %s: response must contain either result or error", server, method)
	}
	if hasError {
		return fmt.Errorf("mcp %s %s: %v", server, method, errObj)
	}
	if value == nil {
		return fmt.Errorf("mcp %s %s: null result", server, method)
	}
	if result == nil {
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, result)
}
