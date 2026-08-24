package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
	"github.com/yyZe0122/yunmengze-agent/internal/version"
)

func (m model) themeCommandCmd(arg string) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(arg) != "" {
			return commandDoneMsg{err: fmt.Errorf("/theme toggles day/night (no args)")}
		}
		return commandDoneMsg{toggleTheme: true}
	}
}

func (m model) skillsCmd(arg string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		arg = strings.TrimSpace(arg)
		if arg != "" {
			parts := strings.Fields(arg)
			action := strings.ToLower(parts[0])
			switch action {
			case "archived":
				skills, err := m.gateway.ListSkillsFilter(ctx, true)
				if err != nil {
					return commandDoneMsg{err: err}
				}
				return commandDoneMsg{openList: listSkills, skills: skills, status: "archived skills · /<id> still selects"}
			case "apply", "reject":
				if len(parts) != 2 {
					return commandDoneMsg{err: fmt.Errorf("usage: /skills %s <skill-id>", action)}
				}
				id := strings.TrimSpace(parts[1])
				var err error
				if action == "apply" {
					err = m.gateway.ApplySkillDraft(ctx, id)
				} else {
					err = m.gateway.RejectSkillDraft(ctx, id)
				}
				if err != nil {
					return commandDoneMsg{err: err}
				}
				return commandDoneMsg{status: fmt.Sprintf("skill %s %s", id, action)}
			default:
				return commandDoneMsg{err: fmt.Errorf("usage: /skills [apply|reject <id>|archived]")}
			}
		}
		skills, err := m.gateway.ListSkills(ctx)
		if err != nil {
			return commandDoneMsg{err: err}
		}
		status := "toggle skills · Enter select · Esc close · /skills apply|reject <id>"
		if n := len(m.selectedSkillIDs); n > 0 {
			status = fmt.Sprintf("%d skill(s) selected · Enter toggle · Esc close", n)
		}
		return commandDoneMsg{openList: listSkills, skills: skills, status: status}
	}
}

func (m model) loadStatusCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		health, err := m.gateway.Health(ctx)
		if err != nil {
			return statusDoneMsg{err: err}
		}
		modelCfg, err := m.gateway.ModelConfig(ctx)
		if err != nil {
			return statusDoneMsg{health: health, err: err}
		}
		msg := statusDoneMsg{health: health, model: modelCfg}
		if skills, err := m.gateway.ListSkills(ctx); err == nil {
			msg.skills = skills
		}
		if commands, err := m.gateway.ListChatCommands(ctx); err == nil {
			msg.commands = commands
		}
		if mcp, mcpErr := m.gateway.MCPStatus(ctx); mcpErr == nil {
			msg.mcp = mcp
			msg.mcpOK = true
		}
		return msg
	}
}

func (m model) statusCommandCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		health, err := m.gateway.Health(ctx)
		if err != nil {
			return commandDoneMsg{err: err}
		}
		modelCfg, _ := m.gateway.ModelConfig(ctx)
		var b strings.Builder
		ver := strings.TrimSpace(health.Core.Version)
		if ver == "" {
			ver = version.Version
		}
		fmt.Fprintf(&b, "health ok=%v version=%s model=%s draft=%s", health.OK, ver, modelCfg.Model, m.draftMode)
		if m.sessionID != "" && m.sessionID != "…" {
			fmt.Fprintf(&b, " session=%s", shortID(string(m.sessionID)))
			if pref := m.sessionPreferredModel(ctx); pref != "" {
				fmt.Fprintf(&b, " prefer=%s", pref)
			}
		}
		if m.task != nil {
			fmt.Fprintf(&b, " task=%s state=%s", shortID(string(m.task.ID)), m.task.State)
		}
		if m.contextOK {
			fmt.Fprintf(&b, " ctx=%d/%d", m.taskContext.LastPromptTokens, m.taskContext.UsableTokens)
			if m.taskContext.Pressure > 0 {
				fmt.Fprintf(&b, " pressure=%.0f%%", m.taskContext.Pressure*100)
			}
			if m.taskContext.Compacted {
				b.WriteString(" compacted")
			}
		}
		sessionID := strings.TrimSpace(string(m.sessionID))
		if sessionID == "…" {
			sessionID = ""
		}
		if perms, err := m.gateway.ListPermissions(ctx, sessionID, 20); err == nil {
			fmt.Fprintf(&b, " perms=%d", len(perms))
		}
		if n := len(m.selectedSkillIDs); n > 0 {
			fmt.Fprintf(&b, " skills=%d", n)
		}
		return commandDoneMsg{status: b.String()}
	}
}

func (m model) modelCommandCmd(arg string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		cfg, err := m.gateway.ModelConfig(ctx)
		if err != nil {
			return commandDoneMsg{err: err}
		}
		pref := m.sessionPreferredModel(ctx)
		arg = strings.TrimSpace(arg)
		if !cfg.Ready && strings.TrimSpace(cfg.Error) != "" {
			if arg == "" {
				return commandDoneMsg{err: fmt.Errorf("model not ready: %s", cfg.Error)}
			}
			// Switching while not ready still goes to SetModelConfig for a concrete error.
		}
		if arg == "" {
			status := "select a model (global main; providerID/modelID…)"
			if pref != "" {
				status = fmt.Sprintf("global=%s · session prefer=%s · /model switches GLOBAL main", cfg.Model, pref)
			}
			if !cfg.Ready && cfg.Error != "" {
				status = "model not ready: " + cfg.Error
			}
			return commandDoneMsg{
				openList:      listModels,
				modelName:     cfg.Model,
				models:        cfg.Models,
				contextWindow: cfg.ContextWindow,
				status:        status,
			}
		}
		// Session prefer (no global switch): /model prefer provider/model  or  /model session provider/model
		fields := strings.Fields(arg)
		if len(fields) >= 1 && (fields[0] == "prefer" || fields[0] == "session") {
			ref := ""
			if len(fields) >= 2 {
				ref = strings.Join(fields[1:], " ")
			}
			return m.setSessionModelPrefCmd(ctx, ref, cfg)
		}
		if !strings.Contains(arg, "/") {
			return commandDoneMsg{err: fmt.Errorf("model must use provider/model format (or /model prefer provider/model for session preference)")}
		}
		if arg == cfg.Model && cfg.Ready {
			status := fmt.Sprintf("already using global %s", cfg.Model)
			if pref != "" && pref != cfg.Model {
				status += fmt.Sprintf(" (session prefer=%s)", pref)
			}
			return commandDoneMsg{
				status:        status,
				modelName:     cfg.Model,
				models:        cfg.Models,
				contextWindow: cfg.ContextWindow,
				closeList:     true,
			}
		}
		updated, err := m.gateway.SetModelConfig(ctx, arg)
		if err != nil {
			return commandDoneMsg{err: err}
		}
		status := fmt.Sprintf("global model=%s", updated.Model)
		if pref != "" && pref != updated.Model {
			status += fmt.Sprintf(" (session prefer=%s still stored)", pref)
		}
		// Also record as session preference when a session is focused (O4).
		if sid := strings.TrimSpace(string(m.sessionID)); sid != "" && sid != "…" {
			if sess, err := m.gateway.SetSessionPreferredModel(ctx, m.sessionID, updated.Model); err == nil && sess.PreferredModel != "" {
				status = fmt.Sprintf("global model=%s · session prefer=%s", updated.Model, sess.PreferredModel)
			}
		}
		return commandDoneMsg{
			status:        status,
			modelName:     updated.Model,
			models:        updated.Models,
			contextWindow: updated.ContextWindow,
			closeList:     true,
		}
	}
}

func (m model) sessionPreferredModel(ctx context.Context) string {
	sid := strings.TrimSpace(string(m.sessionID))
	if sid == "" || sid == "…" {
		return ""
	}
	sess, err := m.gateway.GetSession(ctx, m.sessionID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(sess.PreferredModel)
}

func (m model) setSessionModelPrefCmd(ctx context.Context, ref string, cfg gatewayclient.ModelConfig) tea.Msg {
	sid := strings.TrimSpace(string(m.sessionID))
	if sid == "" || sid == "…" {
		return commandDoneMsg{err: fmt.Errorf("focus a session first, then /model prefer provider/model")}
	}
	ref = strings.TrimSpace(ref)
	if ref != "" && !strings.Contains(ref, "/") {
		return commandDoneMsg{err: fmt.Errorf("preferred model must use provider/model format (or empty to clear)")}
	}
	sess, err := m.gateway.SetSessionPreferredModel(ctx, m.sessionID, ref)
	if err != nil {
		return commandDoneMsg{err: err}
	}
	status := "session preferred model cleared (global still " + cfg.Model + ")"
	if sess.PreferredModel != "" {
		status = fmt.Sprintf("session prefer=%s · global still %s (not switched)", sess.PreferredModel, cfg.Model)
	}
	return commandDoneMsg{
		status:        status,
		modelName:     cfg.Model,
		models:        cfg.Models,
		contextWindow: cfg.ContextWindow,
		closeList:     true,
	}
}
