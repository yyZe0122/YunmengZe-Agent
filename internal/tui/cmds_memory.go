package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

func (m model) journeyCmd(arg string) tea.Cmd {
	return func() tea.Msg {
		sessionID := strings.TrimSpace(string(m.sessionID))
		if sessionID == "" || sessionID == "…" {
			return commandDoneMsg{err: fmt.Errorf("focus a session first (/sessions)")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		want := strings.ToLower(strings.TrimSpace(arg))
		showMem := want == "" || want == "memory"
		showSkills := want == "" || want == "skills"
		if !showMem && !showSkills {
			return commandDoneMsg{err: fmt.Errorf("usage: /journey [memory|skills]")}
		}
		var rows []timelineItem
		memN, skillN := 0, 0
		if showMem {
			entries, err := m.gateway.ListMemory(ctx, sessionID, "", "", 24)
			if err != nil {
				return commandDoneMsg{err: err}
			}
			memN = len(entries)
			if memN > 0 {
				rows = append(rows, timelineItem{
					Kind: tlJourney, Title: "journey · memory",
					Body: fmt.Sprintf("%d entr(y/ies)", memN), Key: "journey:memory:header",
				})
				for i, e := range entries {
					kind := e.Kind
					if kind == "" {
						kind = e.Source
					}
					if kind == "" {
						kind = "fact"
					}
					rows = append(rows, timelineItem{
						Kind: tlJourney, At: e.CreatedAt, Title: fmt.Sprintf("%s · %s", kind, shortID(e.ID)),
						Key: fmt.Sprintf("journey:mem:%d", i),
						Blocks: []contentBlock{{
							Kind: blockPlain, Text: e.Content, Key: fmt.Sprintf("journey:mem:%d:body", i),
						}},
					})
				}
			}
		}
		if showSkills {
			events, err := m.gateway.ListSkillEvents(ctx, "", 24)
			if err != nil {
				return commandDoneMsg{err: err}
			}
			skillN = len(events)
			if skillN > 0 {
				rows = append(rows, timelineItem{
					Kind: tlJourney, Title: "journey · skills",
					Body: fmt.Sprintf("%d event(s)", skillN), Key: "journey:skills:header",
				})
				for i, e := range events {
					body := e.Path
					if e.ContentHash != "" {
						if body != "" {
							body += " · "
						}
						body += shortID(e.ContentHash)
					}
					if body == "" {
						body = e.Actor
					}
					rows = append(rows, timelineItem{
						Kind: tlJourney, At: e.CreatedAt,
						Title: fmt.Sprintf("%s · %s", e.Action, e.SkillID),
						Key:   fmt.Sprintf("journey:sk:%d", i),
						Blocks: []contentBlock{{
							Kind: blockPlain, Text: body, Key: fmt.Sprintf("journey:sk:%d:body", i),
						}},
					})
				}
			}
		}
		if len(rows) == 0 {
			return commandDoneMsg{setJourney: true, journeyRows: nil, status: "journey: no rows"}
		}
		return commandDoneMsg{
			setJourney: true, journeyRows: rows,
			status: fmt.Sprintf("journey: %d memory · %d skill event(s)", memN, skillN),
		}
	}
}

func (m model) refreshMemoryCmd() tea.Cmd {
	return func() tea.Msg {
		sessionID := strings.TrimSpace(string(m.sessionID))
		if sessionID == "…" {
			sessionID = ""
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		if err := m.gateway.RefreshMemory(ctx, sessionID); err != nil {
			return commandDoneMsg{err: err}
		}
		if sessionID == "" {
			return commandDoneMsg{status: "memory snapshot cleared (all sessions); next turns reinject"}
		}
		return commandDoneMsg{status: "memory snapshot refreshed for session; next turn reinjects"}
	}
}

func (m model) memoryCmd(arg string) tea.Cmd {
	return func() tea.Msg {
		arg = strings.TrimSpace(arg)
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		sessionID := strings.TrimSpace(string(m.sessionID))
		if sessionID == "…" {
			sessionID = ""
		}
		if arg == "" {
			entries, err := m.gateway.ListMemory(ctx, sessionID, "", "", 32)
			if err != nil {
				return commandDoneMsg{err: err}
			}
			return commandDoneMsg{status: formatMemoryList(entries)}
		}
		fields := strings.Fields(arg)
		switch strings.ToLower(fields[0]) {
		case "refresh":
			if err := m.gateway.RefreshMemory(ctx, sessionID); err != nil {
				return commandDoneMsg{err: err}
			}
			return commandDoneMsg{status: "memory snapshot refreshed"}
		case "forget":
			if len(fields) < 2 {
				return commandDoneMsg{err: fmt.Errorf("usage: /memory forget <entry_id>")}
			}
			if err := m.gateway.ForgetMemory(ctx, fields[1]); err != nil {
				return commandDoneMsg{err: err}
			}
			return commandDoneMsg{status: "forgot " + fields[1]}
		case "promote":
			if len(fields) < 2 {
				return commandDoneMsg{err: fmt.Errorf("usage: /memory promote <entry_id>")}
			}
			e, err := m.gateway.PromoteMemory(ctx, fields[1])
			if err != nil {
				return commandDoneMsg{err: err}
			}
			return commandDoneMsg{status: fmt.Sprintf("promoted → %s (global curated)", e.ID)}
		case "archived":
			entries, err := m.gateway.ListMemoryFilter(ctx, sessionID, "", "", 32, true)
			if err != nil {
				return commandDoneMsg{err: err}
			}
			return commandDoneMsg{status: formatMemoryList(entries)}
		default:
			// Treat remainder as search query.
			entries, err := m.gateway.ListMemory(ctx, sessionID, arg, "", 32)
			if err != nil {
				return commandDoneMsg{err: err}
			}
			return commandDoneMsg{status: formatMemoryList(entries)}
		}
	}
}

func formatMemoryList(entries []gatewayclient.MemoryEntry) string {
	if len(entries) == 0 {
		return "no memory entries"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d memory entr", len(entries)))
	if len(entries) == 1 {
		b.WriteString("y:\n")
	} else {
		b.WriteString("ies:\n")
	}
	for i, e := range entries {
		if i >= 20 {
			b.WriteString(fmt.Sprintf("… +%d more\n", len(entries)-20))
			break
		}
		scope := e.SessionID
		if scope == "" {
			scope = "global"
		}
		kind := e.Kind
		if kind == "" {
			kind = "?"
		}
		if strings.TrimSpace(e.ArchivedAt) != "" {
			kind += "/archived"
		}
		content := strings.ReplaceAll(e.Content, "\n", " ")
		if len([]rune(content)) > 80 {
			content = string([]rune(content)[:80]) + "…"
		}
		b.WriteString(fmt.Sprintf("  %s [%s/%s] %s\n", e.ID, kind, scope, content))
	}
	return strings.TrimSpace(b.String())
}
