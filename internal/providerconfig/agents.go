package providerconfig

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/injectscan"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/pathsecurity"
)

const AgentsPreamble = "The following user/project agent rules guide this reply. " +
	"Follow the selected execution mode; plan remains read-only regardless of these instructions."

// ReadAgentsFile loads dir/AGENTS.md as a labeled system overlay (fail-closed).
func ReadAgentsFile(dir, label string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	path := filepath.Join(dir, AgentsFilename)
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ""
	}
	if !pathsecurity.ContainsResolved(dir, path) {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 128<<10))
	if err != nil {
		return ""
	}
	text := strings.ReplaceAll(strings.TrimPrefix(string(raw), "\ufeff"), "\r\n", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if n := len([]rune(text)); n > MaxAgentsRunes {
		text = string([]rune(text)[:MaxAgentsRunes])
		slog.Warn("AGENTS.md truncated", "component", "providerconfig", "operation", "agents_inject",
			"result", "warning", "source", label, "runes", n)
	}
	if err := injectscan.Scan(text); err != nil {
		slog.Warn("AGENTS.md inject rejected", "component", "providerconfig", "operation", "agents_inject",
			"result", "warning", "source", label, "error", err)
		return ""
	}
	if label == "" {
		return text
	}
	return "### " + label + "\n" + text
}

// OverlayAgents joins global ConfigDir and project .yunmengze AGENTS.md.
func OverlayAgents(configDir, workspace string) string {
	var parts []string
	if block := ReadAgentsFile(configDir, "global"); block != "" {
		parts = append(parts, block)
	}
	workspace = strings.TrimSpace(workspace)
	if workspace != "" {
		if block := ReadAgentsFile(filepath.Join(workspace, ".yunmengze"), "project"); block != "" {
			parts = append(parts, block)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return AgentsPreamble + "\n\n" + strings.Join(parts, "\n\n")
}
