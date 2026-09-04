package chatsession

import (
	"strings"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/version"
)

func TestChatSystemPromptIncludesVersionAndRoles(t *testing.T) {
	agent := chatSystemPrompt(false, true, nil)
	if !strings.Contains(agent, "YunmengZe Agent "+version.Version) {
		t.Fatalf("agent prompt missing version: %s", agent)
	}
	if !strings.Contains(agent, "build mode") {
		t.Fatalf("agent prompt missing build mode: %s", agent)
	}
	if !strings.Contains(agent, "fs_patch") || !strings.Contains(agent, "fs_remove") {
		t.Fatalf("agent prompt missing write tools: %s", agent)
	}
	if !strings.Contains(agent, "web_search") || !strings.Contains(agent, "web_extract") {
		t.Fatalf("agent prompt missing web tools: %s", agent)
	}
	if !strings.Contains(agent, "vision_analyze") || !strings.Contains(agent, "audio_transcribe") {
		t.Fatalf("agent prompt missing media tools: %s", agent)
	}
	if !strings.Contains(agent, "git restore") {
		t.Fatalf("agent prompt missing git restore hint: %s", agent)
	}
	if !strings.Contains(agent, "/perm") {
		t.Fatalf("interactive agent prompt missing /perm: %s", agent)
	}
	headless := chatSystemPrompt(false, false, nil)
	if strings.Contains(headless, "/perm") {
		t.Fatalf("headless agent prompt must not mention /perm: %s", headless)
	}
	if !strings.Contains(headless, "chat.permission.allow") {
		t.Fatalf("headless agent prompt missing allow hint: %s", headless)
	}
	plan := chatSystemPrompt(true, false, nil)
	if !strings.Contains(plan, "YunmengZe Agent "+version.Version) {
		t.Fatalf("plan prompt missing version: %s", plan)
	}
	if !strings.Contains(plan, "plan mode") {
		t.Fatalf("plan prompt missing plan mode: %s", plan)
	}
	if !strings.Contains(plan, "Read-only") {
		t.Fatalf("plan prompt missing read-only: %s", plan)
	}
	if strings.Contains(plan, "fs_patch") {
		t.Fatalf("plan prompt should not mention write tools: %s", plan)
	}
	for _, got := range []string{agent, plan} {
		for _, want := range []string{
			"models.subagent",
			"models.compact",
			"/model switches global main only",
			"skills_list",
			"session_search",
			"previous task_id",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("prompt missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "Do not claim a vision role") {
			t.Fatalf("prompt still has long vision apology:\n%s", got)
		}
		if strings.Contains(got, "No vision") {
			t.Fatalf("prompt must not hard-code No vision:\n%s", got)
		}
		if strings.Contains(got, "Also configured:") {
			t.Fatalf("prompt listed extra roles with none configured:\n%s", got)
		}
	}
	withVision := chatSystemPrompt(false, true, []string{"vision", "subagent"})
	if !strings.Contains(withVision, "Also configured: models.vision.") {
		t.Fatalf("prompt missing extra role: %s", withVision)
	}
	if strings.Contains(withVision, "Also configured: models.subagent") {
		t.Fatalf("subagent should not be listed as extra: %s", withVision)
	}
}

func TestChatEnvBlock(t *testing.T) {
	got := chatEnvBlock("deepseek1/deepseek-chat", "/tmp/ws", "2026-08-14")
	for _, want := range []string{"<env>", "model: deepseek1/deepseek-chat", "workspace: /tmp/ws", "date: 2026-08-14", "</env>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("env missing %q:\n%s", want, got)
		}
	}
	empty := chatEnvBlock("  ", "", "")
	if !strings.Contains(empty, "model: unknown") || !strings.Contains(empty, "workspace: unknown") || !strings.Contains(empty, "date: unknown") {
		t.Fatalf("empty env = %s", empty)
	}
}
