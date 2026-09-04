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
		sid := strings.TrimSpace(string(m.sessionID))
		ready := sid == "" || sid == "…"
		if !ready {
			fmt.Fprintf(&b, " session=%s", shortID(string(m.sessionID)))
		}
		global := strings.TrimSpace(modelCfg.Model)
		if ready {
			if draft := strings.TrimSpace(m.draftModel); draft != "" && draft != global {
				fmt.Fprintf(&b, " draft_model=%s", draft)
			}
		} else if pref := strings.TrimSpace(m.sessionModel); pref != "" && pref != global {
			fmt.Fprintf(&b, " session_model=%s", pref)
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
		if ready {
			sid = ""
		}
		if perms, err := m.gateway.ListPermissions(ctx, sid, 20); err == nil {
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
		arg = strings.TrimSpace(arg)
		if !cfg.Ready && strings.TrimSpace(cfg.Error) != "" && arg == "" {
			return commandDoneMsg{err: fmt.Errorf("model not ready: %s", cfg.Error)}
		}
		if arg == "" {
			return m.modelPickerMsg(cfg)
		}
		fields := strings.Fields(arg)
		if len(fields) >= 1 && (fields[0] == "prefer" || fields[0] == "session") {
			return commandDoneMsg{err: fmt.Errorf("usage: /model [provider/model]  or  /model main provider/model")}
		}
		if len(fields) >= 1 && fields[0] == "main" {
			ref := strings.TrimSpace(strings.Join(fields[1:], " "))
			if ref == "" {
				return commandDoneMsg{err: fmt.Errorf("usage: /model main provider/model")}
			}
			return m.setGlobalMainMsg(ctx, ref, cfg)
		}
		if !strings.Contains(arg, "/") {
			return commandDoneMsg{err: fmt.Errorf("model must use provider/model format (or /model main provider/model for global)")}
		}
		return m.setSessionModelMsg(ctx, arg, cfg)
	}
}

func (m model) modelPickerMsg(cfg gatewayclient.ModelConfig) commandDoneMsg {
	status := "select a model for this session"
	if sid := strings.TrimSpace(string(m.sessionID)); sid == "" || sid == "…" {
		status = "select a model for the next session · /model main provider/model for global"
	} else if pref := strings.TrimSpace(m.sessionModel); pref != "" {
		status = fmt.Sprintf("session=%s · global=%s · Enter sets this session", pref, cfg.Model)
	} else {
		status = fmt.Sprintf("using global %s · Enter sets this session", cfg.Model)
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

func (m model) setGlobalMainMsg(ctx context.Context, ref string, cfg gatewayclient.ModelConfig) tea.Msg {
	if !strings.Contains(ref, "/") {
		return commandDoneMsg{err: fmt.Errorf("model must use provider/model format")}
	}
	if ref == cfg.Model && cfg.Ready {
		return commandDoneMsg{
			status:        fmt.Sprintf("already using global %s", cfg.Model),
			modelName:     cfg.Model,
			models:        cfg.Models,
			contextWindow: cfg.ContextWindow,
			closeList:     true,
		}
	}
	updated, err := m.gateway.SetModelConfig(ctx, ref)
	if err != nil {
		return commandDoneMsg{err: err}
	}
	return commandDoneMsg{
		status:        fmt.Sprintf("global model=%s", updated.Model),
		modelName:     updated.Model,
		models:        updated.Models,
		contextWindow: updated.ContextWindow,
		closeList:     true,
	}
}

func (m model) setSessionModelMsg(ctx context.Context, ref string, cfg gatewayclient.ModelConfig) tea.Msg {
	sid := strings.TrimSpace(string(m.sessionID))
	if sid == "" || sid == "…" {
		return commandDoneMsg{
			status:        fmt.Sprintf("next session model=%s (global still %s)", ref, cfg.Model),
			modelName:     cfg.Model,
			sessionModel:  ref,
			models:        cfg.Models,
			contextWindow: cfg.ContextWindow,
			closeList:     true,
		}
	}
	sess, err := m.gateway.SetSessionPreferredModel(ctx, m.sessionID, ref)
	if err != nil {
		return commandDoneMsg{err: err}
	}
	shown := strings.TrimSpace(sess.PreferredModel)
	if shown == "" {
		shown = ref
	}
	return commandDoneMsg{
		status:        fmt.Sprintf("session model=%s · global still %s", shown, cfg.Model),
		modelName:     cfg.Model,
		sessionModel:  shown,
		models:        cfg.Models,
		contextWindow: cfg.ContextWindow,
		closeList:     true,
	}
}

func (m model) effectiveModel(global string) string {
	if sid := strings.TrimSpace(string(m.sessionID)); sid != "" && sid != "…" {
		if pref := strings.TrimSpace(m.sessionModel); pref != "" {
			return pref
		}
		return strings.TrimSpace(global)
	}
	if draft := strings.TrimSpace(m.draftModel); draft != "" {
		return draft
	}
	return strings.TrimSpace(global)
}
