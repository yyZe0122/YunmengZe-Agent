package approval

import (
	"errors"
	"testing"
	"time"
)

func TestPlanContainsHTTPGetDomainNarrowing(t *testing.T) {
	plan := PlanDocument{Steps: []StepScope{{
		StepID: "s1",
		Capabilities: []CapabilityScope{{
			Capability: "http_get", MaxDurationMillis: 30000, MaxCalls: 8,
		}},
	}}}
	narrow := CapabilityScope{
		Capability: "http_get", NetworkDomains: []string{"example.com"},
		MaxDurationMillis: 30000, MaxCalls: 8,
	}
	if !planContainsScope(plan, "s1", narrow) {
		t.Fatal("empty-domain plan scope should allow host-narrowed grant")
	}
	wide := CapabilityScope{
		Capability: "http_get", NetworkDomains: []string{"a.com", "b.com"},
		MaxDurationMillis: 30000, MaxCalls: 8,
	}
	if planContainsScope(plan, "s1", wide) {
		t.Fatal("must not widen to multiple domains")
	}
	webPlan := PlanDocument{Steps: []StepScope{{
		StepID: "s1",
		Capabilities: []CapabilityScope{{
			Capability: "web_search", MaxDurationMillis: 30000, MaxCalls: 8,
		}},
	}}}
	webNarrow := CapabilityScope{
		Capability: "web_search", NetworkDomains: []string{"html.duckduckgo.com"},
		MaxDurationMillis: 30000, MaxCalls: 8,
	}
	if !planContainsScope(webPlan, "s1", webNarrow) {
		t.Fatal("web_search empty-domain plan should allow host-narrowed grant")
	}
	pathPlan := PlanDocument{Steps: []StepScope{{
		StepID: "s1",
		Capabilities: []CapabilityScope{{
			Capability: "vision_analyze", Paths: []string{"/tmp/ws"},
			MaxDurationMillis: 30000, MaxCalls: 8,
		}},
	}}}
	pathNarrow := CapabilityScope{
		Capability: "vision_analyze", Paths: []string{"/tmp/ws"},
		NetworkDomains: []string{"example.com"}, MaxDurationMillis: 30000, MaxCalls: 8,
	}
	if planContainsScope(pathPlan, "s1", pathNarrow) {
		t.Fatal("must not host-narrow a path-scoped vision_analyze grant")
	}
}

func TestCommandArgsMatchSchemeA(t *testing.T) {
	tests := []struct {
		name      string
		grantCmd  string
		grantArgs []string
		reqCmd    string
		reqArgs   []string
		wantErr   bool
	}{
		{name: "empty grant accepts any", grantCmd: "", grantArgs: nil, reqCmd: "make", reqArgs: []string{"test"}, wantErr: false},
		{name: "empty grant empty request", grantCmd: "", grantArgs: []string{}, reqCmd: "", reqArgs: nil, wantErr: false},
		{name: "command match args empty grant", grantCmd: "git", grantArgs: nil, reqCmd: "git", reqArgs: []string{"status"}, wantErr: false},
		{name: "command mismatch", grantCmd: "git", grantArgs: nil, reqCmd: "echo", reqArgs: nil, wantErr: true},
		{name: "args exact match", grantCmd: "echo", grantArgs: []string{"hi"}, reqCmd: "echo", reqArgs: []string{"hi"}, wantErr: false},
		{name: "args mismatch when grant non-empty", grantCmd: "echo", grantArgs: []string{"hi"}, reqCmd: "echo", reqArgs: []string{"bye"}, wantErr: true},
		{name: "similar prefix go test", grantCmd: "go", grantArgs: []string{"test"}, reqCmd: "go", reqArgs: []string{"test", "./internal/foo/"}, wantErr: false},
		{name: "similar prefix sh -c", grantCmd: "/bin/sh", grantArgs: []string{"-c", "go test"}, reqCmd: "/bin/sh", reqArgs: []string{"-c", "go test ./internal/foo/"}, wantErr: false},
		{name: "prefix does not match other command", grantCmd: "go", grantArgs: []string{"test"}, reqCmd: "go", reqArgs: []string{"build", "./..."}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := commandArgsMatch(tt.grantCmd, tt.grantArgs, tt.reqCmd, tt.reqArgs)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatal(err)
			}
			if tt.wantErr && err != nil && !errors.Is(err, ErrGrantDenied) {
				t.Fatalf("err = %v, want ErrGrantDenied", err)
			}
		})
	}
}

func TestGrantMatchesRequestPathScopedProcess(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	grant := CapabilityGrant{
		ID: "g1", TaskID: "t1", PlanID: "p1", StepID: "s1", PlanHash: "hash",
		Scope: CapabilityScope{
			Capability: "process_exec", Paths: []string{"/tmp/ws"},
			MaxDurationMillis: 60_000, MaxCalls: 100,
		},
		ExpiresAt: now.Add(time.Hour),
	}
	req := GrantRequest{
		TaskID: "t1", PlanID: "p1", StepID: "s1", PlanHash: "hash",
		Capability: "process_exec", Path: "/tmp/ws", Command: "make", Arguments: []string{"test"},
		Duration: time.Second,
	}
	if err := grantMatchesRequest(grant, req); err != nil {
		t.Fatalf("path-scoped process grant: %v", err)
	}
	req.Path = "/other"
	if err := grantMatchesRequest(grant, req); err == nil {
		t.Fatal("expected path denial")
	}
}

func TestNormalizeProcessExecEmptyCommand(t *testing.T) {
	got, err := NormalizeCapabilityForPlan(CapabilityScope{
		Capability: "process_exec", Paths: []string{"/tmp/ws"},
		MaxDurationMillis: 1000, MaxCalls: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Command != "" {
		t.Fatalf("command = %q", got.Command)
	}
	_, err = NormalizeCapabilityForPlan(CapabilityScope{
		Capability: "process_exec", MaxDurationMillis: 1000, MaxCalls: 1,
	})
	if err == nil {
		t.Fatal("process_exec without paths must fail")
	}
}

func TestNormalizeGitRequiresPaths(t *testing.T) {
	got, err := NormalizeCapabilityForPlan(CapabilityScope{
		Capability: "git_status", Paths: []string{"/tmp/repo"},
		MaxDurationMillis: 1000, MaxCalls: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Capability != "git_status" {
		t.Fatalf("got %#v", got)
	}
	_, err = NormalizeCapabilityForPlan(CapabilityScope{
		Capability: "git_status", MaxDurationMillis: 1000, MaxCalls: 1,
	})
	if err == nil {
		t.Fatal("git without paths must fail")
	}
}
