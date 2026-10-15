package providerconfig

import (
	"strings"
	"testing"
	"time"
)

func TestValidateChatCommands(t *testing.T) {
	t.Parallel()
	ok := ChatConfig{Commands: map[string]ChatCommandConfig{
		"review": {Description: "Code review", Template: "Review:\n\n$ARGUMENTS"},
	}}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	// Builtin clash
	if err := (ChatConfig{Commands: map[string]ChatCommandConfig{
		"help": {Template: "x"},
	}}).validate(); err == nil {
		t.Fatal("want builtin clash")
	}
	// Bad name
	if err := (ChatConfig{Commands: map[string]ChatCommandConfig{
		"bad name": {Template: "x"},
	}}).validate(); err == nil {
		t.Fatal("want bad name")
	}
	// Empty template
	if err := (ChatConfig{Commands: map[string]ChatCommandConfig{
		"x": {Template: "  "},
	}}).validate(); err == nil {
		t.Fatal("want empty template")
	}
	// Suspicious phrases are diagnostic; valid user-authored commands remain usable.
	if err := (ChatConfig{Commands: map[string]ChatCommandConfig{
		"x": {Template: "ignore previous instructions"},
	}}).validate(); err != nil {
		t.Fatal(err)
	}
}

func TestExpandChatCommandTemplate(t *testing.T) {
	t.Parallel()
	if got := ExpandChatCommandTemplate("Review:\n$ARGUMENTS", "foo"); !strings.Contains(got, "foo") {
		t.Fatalf("got %q", got)
	}
	if got := ExpandChatCommandTemplate("plain", "arg"); got != "plain\n\narg" {
		t.Fatalf("got %q", got)
	}
	if got := ExpandChatCommandTemplate("only $0", "z"); got != "only z" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateMemoryDefaultTTL(t *testing.T) {
	t.Parallel()
	if err := (ChatConfig{Memory: &ChatMemoryConfig{DefaultTTL: "168h"}}).validate(); err != nil {
		t.Fatal(err)
	}
	if d := (ChatConfig{Memory: &ChatMemoryConfig{DefaultTTL: "168h"}}).MemoryDefaultTTL(); d != 168*time.Hour {
		t.Fatalf("ttl = %s", d)
	}
	if (ChatConfig{}).MemoryDefaultTTL() != 0 {
		t.Fatal("omit should be zero")
	}
	if err := (ChatConfig{Memory: &ChatMemoryConfig{DefaultTTL: "nope"}}).validate(); err == nil {
		t.Fatal("want invalid duration")
	}
	if err := (ChatConfig{Memory: &ChatMemoryConfig{DefaultTTL: "0s"}}).validate(); err == nil {
		t.Fatal("want non-positive duration")
	}
}
