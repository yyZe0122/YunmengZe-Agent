package tools

import (
	"fmt"
	"strings"
)

const (
	KindGeneral = "general"
	KindExplore = "explore"
	KindWeb     = "web"
	KindVision  = "vision"
	KindSpeech  = "speech"
	KindVideo   = "video"
)

const (
	kindErrUnknown     = "unknown_kind"
	kindErrUnavailable = "kind_unavailable"
	kindErrRole        = "role_unconfigured"
	kindErrForbidden   = "tool_forbidden"
	kindErrEmpty       = "empty_toolset"
)

var (
	readFSTools = []string{"fs_read", "fs_list", "fs_stat", "fs_glob", "fs_grep"}
	leafBans    = []string{"ask_user", "memory_write", "memory_promote", "skill_draft"}
	writeFSBans = []string{"fs_write", "fs_patch", "fs_mkdir", "fs_remove"}
	processBans = []string{"process_exec", "process_shell"}
	httpBans    = []string{"http_get", "web_search", "web_extract"}
)

var exploreDefaultTools = concatTools(readFSTools, []string{
	"skills_list", "skill_view", "session_search", "memory_search", "todo_list",
})

var exploreBans = concatTools(leafBans, writeFSBans, processBans, httpBans)

var webDefaultTools = concatTools(readFSTools, []string{"web_search", "web_extract", "http_get"})

var webBans = concatTools(leafBans, writeFSBans, processBans)

var mediaBans = concatTools(leafBans, writeFSBans, processBans, httpBans)

// KindSpec is the typed sub-agent catalog entry (ADR-039).
type KindSpec struct {
	Kind         string
	Role         string
	FallbackRole string
	ConfigRole   string
	Advertised   bool
	RequireRole  bool
	Default      []string
	Ban          []string
	BanPrefix    []string
	RequireAny   []string
	Prompt       string
}

// ChildSpec is the resolved child run after kind ∩ parent ∩ request.
type ChildSpec struct {
	Kind   string
	Role   string
	Tools  []string
	Prompt string
}

type kindResolveError struct {
	Code    string
	Kind    string
	Message string
	Tools   []string
}

func (e *kindResolveError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

var kindCatalog = []KindSpec{
	{
		Kind:       KindGeneral,
		Role:       "subagent",
		Advertised: true,
		Ban:        append([]string(nil), leafBans...),
		Prompt:     "Complete the delegated task. Reply helpfully in the user's language. Prefer absolute paths under the workspace. Do not claim tool success without evidence. You cannot spawn nested task runs, call ask_user, write memory, or draft skills.",
	},
	{
		Kind:       KindExplore,
		Role:       "subagent",
		Advertised: true,
		Default:    append([]string(nil), exploreDefaultTools...),
		Ban:        append([]string(nil), exploreBans...),
		BanPrefix:  []string{"git_", "mcp_"},
		Prompt:     "Read-only exploration. Search the workspace and report findings. Do not modify files, run processes, or make network requests. You cannot spawn nested task runs. Reply helpfully in the user's language. Prefer absolute paths under the workspace. Do not claim tool success without evidence.",
	},
	{
		Kind:         KindWeb,
		Role:         "web",
		FallbackRole: "subagent",
		Advertised:   true,
		Default:      append([]string(nil), webDefaultTools...),
		Ban:          append([]string(nil), webBans...),
		BanPrefix:    []string{"git_", "mcp_"},
		RequireAny:   []string{"web_search", "web_extract", "http_get"},
		Prompt:       "Gather information from the web and report findings. Use web_search, then web_extract or http_get for specific URLs. Do not modify files or run processes. You cannot spawn nested task runs.",
	},
	{
		Kind:        KindVision,
		Role:        "vision",
		RequireRole: true,
		Default:     concatTools(readFSTools, []string{"vision_analyze"}),
		Ban:         append([]string(nil), mediaBans...),
		BanPrefix:   []string{"git_", "mcp_"},
		RequireAny:  []string{"vision_analyze"},
		Prompt:      "Analyze images using vision_analyze and report findings. Do not modify files or run processes. You cannot spawn nested task runs.",
	},
	{
		Kind:        KindSpeech,
		Role:        "subagent",
		ConfigRole:  "speech",
		RequireRole: true,
		Default:     concatTools(readFSTools, []string{"audio_transcribe"}),
		Ban:         append([]string(nil), mediaBans...),
		BanPrefix:   []string{"git_", "mcp_"},
		RequireAny:  []string{"audio_transcribe"},
		Prompt:      "Transcribe audio using audio_transcribe and report the text. Do not modify files or run processes. You cannot spawn nested task runs.",
	},
	{
		Kind:        KindVideo,
		Role:        "vision",
		RequireRole: true,
		Default:     concatTools(readFSTools, []string{"video_analyze"}),
		Ban:         append([]string(nil), mediaBans...),
		BanPrefix:   []string{"git_", "mcp_"},
		RequireAny:  []string{"video_analyze"},
		Prompt:      "Analyze video using video_analyze and report findings. Do not modify files or run processes. You cannot spawn nested task runs.",
	},
}

func lookupKind(kind string) (KindSpec, bool) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = KindGeneral
	}
	for _, spec := range kindCatalog {
		if spec.Kind == kind {
			return spec, true
		}
	}
	return KindSpec{}, false
}

// AdvertisedKinds returns kind names currently shown on the task tool.
func AdvertisedKinds() []string {
	return AdvertisedKindsFor(nil)
}

// AdvertisedKindsFor returns kinds advertised given configured models.* roles.
func AdvertisedKindsFor(configured map[string]struct{}) []string {
	out := make([]string, 0, 4)
	for _, spec := range kindCatalog {
		if spec.Advertised {
			out = append(out, spec.Kind)
			continue
		}
		if spec.RequireRole && roleConfigured(configured, spec.configRole()) {
			out = append(out, spec.Kind)
		}
	}
	return out
}

func (spec KindSpec) forbids(name string) bool {
	if name == "task" {
		return true
	}
	for _, ban := range spec.Ban {
		if name == ban {
			return true
		}
	}
	for _, prefix := range spec.BanPrefix {
		if prefix != "" && strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func (spec KindSpec) configRole() string {
	if role := strings.TrimSpace(spec.ConfigRole); role != "" {
		return role
	}
	return strings.TrimSpace(spec.Role)
}

func resolveChildRole(spec KindSpec, configured map[string]struct{}) (string, error) {
	role := strings.TrimSpace(spec.Role)
	if spec.RequireRole {
		gate := spec.configRole()
		if !roleConfigured(configured, gate) {
			return "", &kindResolveError{
				Code:    kindErrRole,
				Kind:    spec.Kind,
				Message: fmt.Sprintf("kind %q requires models.%s; it is not configured", spec.Kind, gate),
			}
		}
		if role == "" {
			return "subagent", nil
		}
		return role, nil
	}
	if spec.FallbackRole != "" && !roleConfigured(configured, role) {
		return spec.FallbackRole, nil
	}
	if role == "" {
		return "subagent", nil
	}
	return role, nil
}

func kindAdvertised(spec KindSpec, configured map[string]struct{}) bool {
	if spec.Advertised {
		return true
	}
	return spec.RequireRole && roleConfigured(configured, spec.configRole())
}

func roleConfigured(configured map[string]struct{}, role string) bool {
	if role == "" || configured == nil {
		return false
	}
	_, ok := configured[role]
	return ok
}

// ResolveChildTools intersects kind defaults/bans with the parent allow-list.
func ResolveChildTools(kind string, parent, requested []string, configured map[string]struct{}) (ChildSpec, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = KindGeneral
	}
	spec, ok := lookupKind(kind)
	if !ok {
		return ChildSpec{}, &kindResolveError{
			Code:    kindErrUnknown,
			Kind:    kind,
			Message: fmt.Sprintf("unknown task kind %q", kind),
		}
	}
	if !kindAdvertised(spec, configured) {
		return ChildSpec{}, &kindResolveError{
			Code:    kindErrUnavailable,
			Kind:    spec.Kind,
			Message: fmt.Sprintf("task kind %q is not available", spec.Kind),
		}
	}
	role, err := resolveChildRole(spec, configured)
	if err != nil {
		return ChildSpec{}, err
	}
	parentOrder, parentSet := uniqueToolSet(parent)
	req := uniqueTools(requested)
	var tools []string
	if len(req) == 0 {
		candidates := parentOrder
		if len(spec.Default) > 0 {
			candidates = spec.Default
		}
		tools = make([]string, 0, len(candidates))
		for _, name := range candidates {
			if spec.forbids(name) {
				continue
			}
			if _, ok := parentSet[name]; !ok {
				continue
			}
			tools = append(tools, name)
		}
	} else {
		var forbidden []string
		tools = make([]string, 0, len(req))
		seen := make(map[string]struct{}, len(req))
		for _, name := range req {
			if spec.forbids(name) {
				forbidden = append(forbidden, name)
				continue
			}
			if _, ok := parentSet[name]; !ok {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			tools = append(tools, name)
		}
		if len(forbidden) > 0 {
			return ChildSpec{}, &kindResolveError{
				Code:    kindErrForbidden,
				Kind:    spec.Kind,
				Message: fmt.Sprintf("kind %q forbids tools %s", spec.Kind, strings.Join(forbidden, ", ")),
				Tools:   forbidden,
			}
		}
	}
	if len(tools) == 0 {
		return ChildSpec{}, &kindResolveError{
			Code:    kindErrEmpty,
			Kind:    spec.Kind,
			Message: fmt.Sprintf("kind %q has no allowed tools after intersecting with the parent run", spec.Kind),
		}
	}
	if !kindHasRequiredTool(spec, tools) {
		return ChildSpec{}, &kindResolveError{
			Code:    kindErrEmpty,
			Kind:    spec.Kind,
			Message: fmt.Sprintf("kind %q needs one of [%s] on the parent run", spec.Kind, strings.Join(spec.RequireAny, ", ")),
		}
	}
	return ChildSpec{Kind: spec.Kind, Role: role, Tools: tools, Prompt: spec.Prompt}, nil
}

func kindHasRequiredTool(spec KindSpec, tools []string) bool {
	if len(spec.RequireAny) == 0 {
		return true
	}
	have := make(map[string]struct{}, len(tools))
	for _, name := range tools {
		have[name] = struct{}{}
	}
	for _, name := range spec.RequireAny {
		if _, ok := have[name]; ok {
			return true
		}
	}
	return false
}

func uniqueTools(names []string) []string {
	order, _ := uniqueToolSet(names)
	return order
}

func uniqueToolSet(names []string) ([]string, map[string]struct{}) {
	set := make(map[string]struct{}, len(names))
	order := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, exists := set[name]; exists {
			continue
		}
		set[name] = struct{}{}
		order = append(order, name)
	}
	return order, set
}

func concatTools(parts ...[]string) []string {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]string, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
