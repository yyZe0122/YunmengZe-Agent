package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

const defaultFileReadBytes = 2 * 1024 * 1024

type fileTool struct {
	name        string
	guard       *PathGuard
	checkpoints EditCheckpointer
}

// EditCheckpointer snapshots file bytes before write/remove (QG). Fail-closed.
type EditCheckpointer interface {
	SnapshotBeforeWrite(ctx context.Context, path string, before []byte, shaAfter, kind string) error
}

func newFileTools(guard *PathGuard) []Tool {
	return newFileToolsWithCheckpoint(guard, nil)
}

func newFileToolsWithCheckpoint(guard *PathGuard, cp EditCheckpointer) []Tool {
	names := []string{"fs_read", "fs_list", "fs_stat", "fs_glob", "fs_grep", "fs_write", "fs_patch", "fs_mkdir", "fs_remove"}
	result := make([]Tool, 0, len(names))
	for _, name := range names {
		result = append(result, &fileTool{name: name, guard: guard, checkpoints: cp})
	}
	return result
}

func (t *fileTool) SetCheckpointer(cp EditCheckpointer) {
	if t != nil {
		t.checkpoints = cp
	}
}

func (t *fileTool) Definition() toolapi.Definition {
	risk := policy.RiskR0
	if t.name == "fs_write" || t.name == "fs_patch" || t.name == "fs_mkdir" || t.name == "fs_remove" {
		risk = policy.RiskR1
	}
	return toolapi.Definition{
		Name: t.name, Description: fileDescription(t.name), InputSchema: json.RawMessage(fileSchema(t.name)),
		Risk: string(risk), DefaultTimeoutMillis: 5000,
	}
}

func (t *fileTool) Authorization(ctx context.Context, raw json.RawMessage) (Authorization, error) {
	path, err := t.authorizationPath(raw)
	if err != nil {
		return Authorization{}, err
	}
	resolved, extra, err := t.guard.ResolveForAuth(ctx, path)
	if err != nil {
		return Authorization{}, err
	}
	return Authorization{Capability: t.name, Path: resolved, ExtraRoot: extra}, nil
}

func (t *fileTool) authorizationPath(raw json.RawMessage) (string, error) {
	var path string
	switch t.name {
	case "fs_read":
		var input readInput
		if err := decodeStrict(raw, &input); err != nil {
			return "", err
		}
		path = input.Path
	case "fs_write":
		var input writeInput
		if err := decodeStrict(raw, &input); err != nil {
			return "", err
		}
		path = input.Path
	case "fs_patch":
		var input patchInput
		if err := decodeStrict(raw, &input); err != nil {
			return "", err
		}
		path = input.Path
	case "fs_list", "fs_stat", "fs_mkdir", "fs_remove":
		var input struct {
			Path string `json:"path"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return "", err
		}
		path = input.Path
	case "fs_glob":
		var input globInput
		if err := decodeStrict(raw, &input); err != nil {
			return "", err
		}
		path = input.Path
		if strings.TrimSpace(path) == "" {
			path = "."
		}
	case "fs_grep":
		var input grepInput
		if err := decodeStrict(raw, &input); err != nil {
			return "", err
		}
		path = input.Path
		if strings.TrimSpace(path) == "" {
			path = "."
		}
	default:
		return "", ErrUnknownTool
	}
	return path, nil
}

func (t *fileTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch t.name {
	case "fs_read":
		return t.read(ctx, raw)
	case "fs_list":
		return t.list(ctx, raw)
	case "fs_stat":
		return t.stat(ctx, raw)
	case "fs_glob":
		return t.glob(ctx, raw)
	case "fs_grep":
		return t.grep(ctx, raw)
	case "fs_write":
		return t.write(ctx, raw)
	case "fs_patch":
		return t.patch(ctx, raw)
	case "fs_mkdir":
		return t.mkdir(ctx, raw)
	case "fs_remove":
		return t.remove(ctx, raw)
	default:
		return nil, ErrUnknownTool
	}
}

func fileDescription(name string) string {
	const pathHint = " Prefer absolute paths. Relative paths resolve against the workspace root. In interactive TUI, an absolute path outside the workspace prompts /perm (once / this session / write chat.workspace.allow / deny) — do not tell the user to edit agent.json."
	return map[string]string{
		"fs_read":   "Read a text file with 1-based line numbers. Default ~2000 lines; use offset/limit. Returns sha256. Prefer over whole-file dumps." + pathHint,
		"fs_list":   "List a directory. Interactive TUI may prompt to approve an extra absolute root." + pathHint,
		"fs_stat":   "Read file metadata inside configured filesystem roots." + pathHint,
		"fs_glob":   "Match files under a directory. ** is recursive (depth-capped). Prefer over shell find." + pathHint,
		"fs_grep":   "Search file contents under a path (literal or simple regex). Prefer over process_exec grep." + pathHint,
		"fs_write":  "Atomically write a file. Prefer fs_patch for edits. Optional expected_sha256 optimistic lock. Returns unified diff." + pathHint,
		"fs_patch":  "Replace text (old/new) after fs_read. Pass expected_sha256 from that read. Tolerates CRLF, indent, and line trim. Failure returns ±20 line context. Success returns unified diff." + pathHint,
		"fs_mkdir":  "Create a directory inside configured filesystem roots." + pathHint,
		"fs_remove": "Delete one regular file (not a directory). Prefer over shell rm so /undo can restore it." + pathHint,
	}[name]
}

func fileSchema(name string) string {
	pathProp := `{"type":"string","description":"Prefer absolute path under configured roots; relative paths resolve against the workspace root."}`
	schemas := map[string]string{
		"fs_read":   `{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":` + pathProp + `,"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1,"maximum":10000},"max_bytes":{"type":"integer","minimum":1}}}`,
		"fs_list":   `{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":` + pathProp + `}}`,
		"fs_stat":   `{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":` + pathProp + `}}`,
		"fs_glob":   `{"type":"object","additionalProperties":false,"required":["pattern"],"properties":{"pattern":{"type":"string"},"path":` + pathProp + `,"max":{"type":"integer","minimum":1,"maximum":1000}}}`,
		"fs_grep":   `{"type":"object","additionalProperties":false,"required":["pattern"],"properties":{"pattern":{"type":"string"},"path":` + pathProp + `,"glob":{"type":"string"},"max_matches":{"type":"integer","minimum":1,"maximum":200},"max_files":{"type":"integer","minimum":1,"maximum":500},"literal":{"type":"boolean"},"case_insensitive":{"type":"boolean"}}}`,
		"fs_write":  `{"type":"object","additionalProperties":false,"required":["path","content"],"properties":{"path":` + pathProp + `,"content":{"type":"string"},"expected_sha256":{"type":"string"}}}`,
		"fs_patch":  `{"type":"object","additionalProperties":false,"required":["path","old","new"],"properties":{"path":` + pathProp + `,"old":{"type":"string"},"new":{"type":"string"},"replace_all":{"type":"boolean"},"expected_sha256":{"type":"string"}}}`,
		"fs_mkdir":  `{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":` + pathProp + `}}`,
		"fs_remove": `{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":` + pathProp + `}}`,
	}
	return schemas[name]
}
