package tui

import (
	"strings"
)

type slashCommand struct {
	Name string
	Desc string
	Help string
}

var slashCommands = []slashCommand{
	{Name: "/new", Desc: "leave session", Help: "/new  leave session (cancels a running turn)"},
	{Name: "/sessions", Desc: "list chat sessions", Help: "/sessions  list sessions (open with Enter)"},
	{Name: "/tasks", Desc: "list / focus tasks", Help: "/tasks [id-prefix]  list tasks or focus by id"},
	{Name: "/back", Desc: "session list", Help: "/back  open session list"},
	{Name: "/clear", Desc: "session list", Help: "/clear  open session list"},
	{Name: "/pause", Desc: "pause task", Help: "/pause [reason]  pause current task"},
	{Name: "/resume", Desc: "resume task", Help: "/resume  resume current task"},
	{Name: "/cancel", Desc: "cancel task", Help: "/cancel [reason]  cancel current task (/stop)"},
	{Name: "/stop", Desc: "cancel task", Help: "/stop [reason]  alias for /cancel"},
	{Name: "/retry", Desc: "resubmit last user message", Help: "/retry  resubmit last user message on focused session"},
	{Name: "/model", Desc: "list or switch global model", Help: "/model [provider/model]  global main; /model prefer [ref] session prefer (next run)"},
	{Name: "/skills", Desc: "select skills for next submit", Help: "/skills  toggle; /skills apply|reject <id>; /skills archived"},
	{Name: "/theme", Desc: "toggle day/night theme", Help: "/theme  toggle day ↔ night theme"},
	{Name: "/cron", Desc: "list or create scheduled jobs", Help: "/cron [every objective]  list jobs, or create on current session (Tab mode)"},
	{Name: "/compact", Desc: "compact session context", Help: "/compact [focus]  force session head summary (optional focus text)"},
	{Name: "/undo", Desc: "rewind last file edit", Help: "/undo  restore last agent file write (Esc Esc)"},
	{Name: "/perm", Desc: "tool permission queue", Help: "/perm open; keys 1–4 once|similar|permanent|deny; /perm <decision> <id>"},
	{Name: "/expand", Desc: "expand/collapse folded blocks", Help: "/expand [all|none|last]  or keys e (last) · E (all) · c (collapse)"},
	{Name: "/journey", Desc: "memory/skill timeline", Help: "/journey [memory|skills]  prepend read-only memory and/or skill events"},
	{Name: "/memory", Desc: "list/search local memory", Help: "/memory [q…]  list facts; /memory archived; /memory forget|promote <id>; /memory refresh"},
	{Name: "/refresh-memory", Desc: "rebuild frozen memory inject", Help: "/refresh-memory  invalidate session memory snapshot (next turn reinjects)"},
	{Name: "/status", Desc: "health summary", Help: "/status  health + version + model + task + context + pending perms"},
	{Name: "/help", Desc: "command list", Help: "/help  command list"},
	{Name: "/quit", Desc: "exit", Help: "/quit  exit TUI (/q /exit)"},
}

// canonicalSlash maps aliases to primary command names.
func canonicalSlash(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "/q", "/exit":
		return "/quit"
	case "/clear", "/resume-list":
		return "/back"
	case "/sessions":
		return "/sessions"
	case "/stop":
		return "/cancel"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

// isBuiltinSlash reports whether name is a fixed TUI command (not skill-as-slash).
func isBuiltinSlash(name string) bool {
	name = canonicalSlash(name)
	if name == "" || name[0] != '/' {
		return false
	}
	for _, cmd := range slashCommands {
		if canonicalSlash(cmd.Name) == name {
			return true
		}
	}
	// Aliases already canonicalized above.
	return false
}

// skillSlashName returns "/id" for a skill id (empty if invalid).
func skillSlashName(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return "/" + id
}

func helpText() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("Commands") + "\n")
	for _, cmd := range slashCommands {
		b.WriteString("  ")
		b.WriteString(paintKeywords(cmd.Help))
		b.WriteByte('\n')
	}
	b.WriteString("\n")
	b.WriteString(styleTitle.Render("Keys") + "\n")
	b.WriteString("  " + styleKeyword.Render("Tab") + "            complete slash, else cycle plan→agent→auto\n")
	b.WriteString("  " + styleKeyword.Render("Shift+Tab") + "      cycle auto→agent→plan\n")
	b.WriteString("  ↑↓             picker / completer / history (not chat)\n")
	b.WriteString("  " + styleKeyword.Render("PgUp") + "/" + styleKeyword.Render("PgDn") + "      scroll this conversation\n")
	b.WriteString("  " + styleKeyword.Render("Ctrl+PgUp") + "/" + styleKeyword.Render("Ctrl+PgDn") + "  older/newer session\n")
	b.WriteString("  " + styleKeyword.Render("e") + " / " + styleKeyword.Render("E") + " / " + styleKeyword.Render("c") + "      expand last foldable · expand all · collapse (empty input)\n")
	b.WriteString("  " + styleKeyword.Render("Enter") + "          complete slash once, then execute; open picker item\n")
	b.WriteString("  " + styleKeyword.Render("Shift+Enter") + "    newline in editor\n")
	b.WriteString("  " + styleKeyword.Render("Esc") + "            close overlay; running → cancel; Esc Esc undo last file edit\n")
	b.WriteString("  " + styleKeyword.Render("Ctrl+P") + "/" + styleKeyword.Render("L") + "/" + styleKeyword.Render("S") + "/" + styleKeyword.Render("T") + "   commands · models · sessions · todos\n")
	b.WriteString("  " + styleKeyword.Render("Ctrl+C") + "         clear input (exit via /quit only)\n")
	b.WriteString("\n")
	b.WriteString(styleTitle.Render("Modes") + styleDim.Render(" (input rule color)") + "\n")
	b.WriteString("  " + styleModePlan.Render("plan") + "   read-only analysis\n")
	b.WriteString("  " + styleModeAgent.Render("agent") + "  build/write; ungranted tests/git wait on /perm\n")
	b.WriteString("  " + styleModeAuto.Render("auto") + "   this session pre-grants process+git (leave to end)\n")
	b.WriteString("\n")
	b.WriteString(styleTitle.Render("Chat") + "\n")
	b.WriteString("  Plain text continues the current session (or starts one).\n")
	b.WriteString("  While a turn is running, Enter steers the next step (does not cancel tools).\n")
	b.WriteString("  " + paintKeywords("/new") + " leaves to ready and cancels a running turn. Type to start a session. Tab sets plan|agent|auto.\n")
	b.WriteString("  " + paintKeywords("/skills preloads instruction skills for the next submit (explicit snapshot).") + "\n")
	b.WriteString("  Other skills: model calls skills_list then skill_view. " + paintKeywords("/<skill-id>") + " toggles preload.\n")
	b.WriteString("  chat.commands templates: " + paintKeywords("/<cmd>") + " [args] expand $ARGUMENTS and submit (instruction only).\n")
	b.WriteString("  Priority: built-in > chat.commands > skill ids.\n")
	b.WriteString("  " + paintKeywords("/perm opens pending tool permissions in Agent (ungranted high-risk).") + "\n")
	b.WriteString("  " + paintKeywords("/retry resubmits the last user message on the focused session.") + "\n")
	return b.String()
}

func paintKeywords(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); {
		if runes[i] == '/' && (i == 0 || !slashNameRune(runes[i-1])) {
			j := i + 1
			if j < len(runes) && runes[j] == '<' {
				for j < len(runes) && runes[j] != '>' {
					j++
				}
				if j < len(runes) {
					j++
				}
				b.WriteString(styleKeyword.Render(string(runes[i:j])))
				i = j
				continue
			}
			for j < len(runes) && slashNameRune(runes[j]) {
				j++
			}
			if j > i+1 {
				b.WriteString(styleKeyword.Render(string(runes[i:j])))
				i = j
				continue
			}
		}
		b.WriteRune(runes[i])
		i++
	}
	return b.String()
}

func slashNameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '-' || r == '_'
}

func parseSlash(line string) (name, arg string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", ""
	}
	if !strings.HasPrefix(line, "/") {
		return "", line
	}
	parts := strings.SplitN(line, " ", 2)
	name = canonicalSlash(parts[0])
	if len(parts) == 2 {
		arg = strings.TrimSpace(parts[1])
	}
	return name, arg
}
