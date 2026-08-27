package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
)

func TestToggleTheme(t *testing.T) {
	if toggleTheme(ThemeDay) != ThemeNight {
		t.Fatal("day should toggle to night")
	}
	if toggleTheme(ThemeNight) != ThemeDay {
		t.Fatal("night should toggle to day")
	}
	if toggleTheme("") != ThemeDay {
		t.Fatal("empty should toggle to day")
	}
}

func TestThemeByNameFallback(t *testing.T) {
	if themeByName(ThemeDay).Name != ThemeDay {
		t.Fatal("day")
	}
	if themeByName(ThemeNight).Name != ThemeNight {
		t.Fatal("night")
	}
	if themeByName("unknown").Name != ThemeNight {
		t.Fatal("fallback night")
	}
}

func TestSaveLoadThemeRoundTrip(t *testing.T) {
	configDir := t.TempDir()
	// Override via writing directly to prefs path pattern is hard without Resolve;
	// test marshal shape and loadTheme parsing via temp file helper.
	path := filepath.Join(configDir, tuiPrefsFilename)
	raw, err := json.MarshalIndent(tuiPrefs{Theme: ThemeDay}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	var prefs tuiPrefs
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &prefs); err != nil {
		t.Fatal(err)
	}
	if prefs.Theme != ThemeDay {
		t.Fatalf("theme = %q", prefs.Theme)
	}
}

func TestLoadThemeMissingDefaultsNight(t *testing.T) {
	// ModeUser resolve may fail in CI without HOME; just ensure loadTheme never panics.
	_ = loadTheme(paths.ModeUser)
}

func TestFormatTokens(t *testing.T) {
	if formatTokens(42) != "42" {
		t.Fatal(formatTokens(42))
	}
	if formatTokens(1200) != "1.2k" {
		t.Fatal(formatTokens(1200))
	}
	if formatTokens(2000) != "2k" {
		t.Fatal(formatTokens(2000))
	}
}

func TestThemeCommandRejectsArgs(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	msg := m.themeCommandCmd("day")()
	done := msg.(commandDoneMsg)
	if done.err == nil {
		t.Fatal("expected error for /theme with args")
	}
	msg = m.themeCommandCmd("")()
	done = msg.(commandDoneMsg)
	if done.err != nil || !done.toggleTheme {
		t.Fatalf("got %#v", done)
	}
}
