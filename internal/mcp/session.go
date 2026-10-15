// Package mcp implements on-demand stdio and remote MCP clients.
package mcp

import (
	"context"
	"encoding/json"
	"strconv"
)

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
		return int64(v) == want
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
		return strconv.FormatInt(want, 10) == formatRawID(raw)
	}
}

func formatRawID(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
