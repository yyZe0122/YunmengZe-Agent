package tools

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestAdvertisedKinds(t *testing.T) {
	got := AdvertisedKinds()
	if !slices.Equal(got, []string{KindGeneral, KindExplore, KindWeb}) {
		t.Fatalf("advertised = %v", got)
	}
}

func TestResolveChildToolsGeneralDefault(t *testing.T) {
	parent := []string{"fs_read", "fs_write", "task", "ask_user", "memory_write", "mcp_foo", "todo_list"}
	child, err := ResolveChildTools("", parent, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if child.Kind != KindGeneral || child.Role != "subagent" {
		t.Fatalf("child = %+v", child)
	}
	if slices.Contains(child.Tools, "task") || slices.Contains(child.Tools, "ask_user") || slices.Contains(child.Tools, "memory_write") {
		t.Fatalf("leaf bans leaked: %v", child.Tools)
	}
	if !slices.Contains(child.Tools, "fs_write") || !slices.Contains(child.Tools, "mcp_foo") || !slices.Contains(child.Tools, "todo_list") {
		t.Fatalf("general should keep parent writes and mcp: %v", child.Tools)
	}
}

func TestResolveChildToolsExploreDefault(t *testing.T) {
	parent := []string{"fs_read", "fs_grep", "fs_write", "process_exec", "http_get", "git_status", "mcp_foo", "skills_list", "task", "todo_list"}
	child, err := ResolveChildTools(KindExplore, parent, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"fs_read", "fs_grep", "skills_list", "todo_list"}
	if !slices.Equal(child.Tools, want) {
		t.Fatalf("explore tools = %v want %v", child.Tools, want)
	}
}

func TestResolveChildToolsRequestedSubset(t *testing.T) {
	parent := []string{"fs_read", "fs_list", "fs_grep", "task"}
	child, err := ResolveChildTools(KindExplore, parent, []string{"fs_grep", "fs_list", "fs_stat"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(child.Tools, []string{"fs_grep", "fs_list"}) {
		t.Fatalf("subset = %v", child.Tools)
	}
}

func TestResolveChildToolsForbiddenRequest(t *testing.T) {
	parent := []string{"fs_read", "process_exec", "task"}
	_, err := ResolveChildTools(KindExplore, parent, []string{"process_exec"}, nil)
	var kr *kindResolveError
	if !errors.As(err, &kr) || kr.Code != kindErrForbidden {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveChildToolsUnknownAndUnadvertised(t *testing.T) {
	parent := []string{"fs_read"}
	_, err := ResolveChildTools("orchestrator", parent, nil, nil)
	var kr *kindResolveError
	if !errors.As(err, &kr) || kr.Code != kindErrUnknown {
		t.Fatalf("unknown: %v", err)
	}
	_, err = ResolveChildTools(KindVision, parent, nil, nil)
	if !errors.As(err, &kr) || kr.Code != kindErrUnavailable {
		t.Fatalf("vision: %v", err)
	}
}

func TestResolveChildToolsWeb(t *testing.T) {
	parent := []string{"fs_read", "web_search", "web_extract", "http_get", "fs_write", "task"}
	child, err := ResolveChildTools(KindWeb, parent, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if child.Kind != KindWeb || child.Role != "subagent" {
		t.Fatalf("child = %+v", child)
	}
	if slices.Contains(child.Tools, "fs_write") || slices.Contains(child.Tools, "task") {
		t.Fatalf("web leaked writes: %v", child.Tools)
	}
	if !slices.Contains(child.Tools, "web_search") || !slices.Contains(child.Tools, "http_get") {
		t.Fatalf("web missing search tools: %v", child.Tools)
	}
	_, err = ResolveChildTools(KindWeb, []string{"fs_read", "task"}, nil, nil)
	var kr *kindResolveError
	if !errors.As(err, &kr) || kr.Code != kindErrEmpty {
		t.Fatalf("plan web: %v", err)
	}
	if !strings.Contains(kr.Message, "web_search") || strings.Contains(kr.Message, "needs a web tool") {
		t.Fatalf("web empty message = %q", kr.Message)
	}
	_, err = ResolveChildTools(KindVision, []string{"fs_read", "task"}, nil, map[string]struct{}{"vision": {}})
	if !errors.As(err, &kr) || kr.Code != kindErrEmpty || !strings.Contains(kr.Message, "vision_analyze") {
		t.Fatalf("vision empty: %v", err)
	}
	child, err = ResolveChildTools(KindWeb, parent, nil, map[string]struct{}{"web": {}})
	if err != nil || child.Role != "web" {
		t.Fatalf("configured web role = %+v err=%v", child, err)
	}
}

func TestResolveChildToolsEmptyAfterIntersect(t *testing.T) {
	_, err := ResolveChildTools(KindGeneral, []string{"task", "ask_user"}, nil, nil)
	var kr *kindResolveError
	if !errors.As(err, &kr) || kr.Code != kindErrEmpty {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveChildRoleRequireAndFallback(t *testing.T) {
	vision, ok := lookupKind(KindVision)
	if !ok || !vision.RequireRole {
		t.Fatalf("vision spec = %+v", vision)
	}
	_, err := resolveChildRole(vision, nil)
	var kr *kindResolveError
	if !errors.As(err, &kr) || kr.Code != kindErrRole {
		t.Fatalf("unconfigured vision: %v", err)
	}
	role, err := resolveChildRole(vision, map[string]struct{}{"vision": {}})
	if err != nil || role != "vision" {
		t.Fatalf("configured vision role = %q err=%v", role, err)
	}
	web, ok := lookupKind(KindWeb)
	if !ok {
		t.Fatal("missing web kind")
	}
	role, err = resolveChildRole(web, nil)
	if err != nil || role != "subagent" {
		t.Fatalf("web fallback = %q err=%v", role, err)
	}
	role, err = resolveChildRole(web, map[string]struct{}{"web": {}})
	if err != nil || role != "web" {
		t.Fatalf("web configured = %q err=%v", role, err)
	}
	speech, ok := lookupKind(KindSpeech)
	if !ok || !speech.RequireRole || speech.configRole() != "speech" {
		t.Fatalf("speech spec = %+v", speech)
	}
	_, err = resolveChildRole(speech, nil)
	if !errors.As(err, &kr) || kr.Code != kindErrRole {
		t.Fatalf("unconfigured speech: %v", err)
	}
	role, err = resolveChildRole(speech, map[string]struct{}{"speech": {}})
	if err != nil || role != "subagent" {
		t.Fatalf("configured speech role = %q err=%v", role, err)
	}
}
