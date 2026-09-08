import type { HostToWebview, Snapshot, WebviewToHost } from "../chat/protocol"
import {
  cycleStance,
  type Chip,
  type Job,
  type LiveState,
  type MemoryEntry,
  type Permission,
  type Stance,
  type TranscriptMessage,
  type TranscriptToolCall,
  type UserQuestion,
} from "../types"

declare function acquireVsCodeApi(): {
  postMessage(msg: WebviewToHost): void
  getState(): { draft?: string; sessionId?: string } | undefined
  setState(s: { draft?: string; sessionId?: string }): void
}

const vscode = acquireVsCodeApi()
const app = document.getElementById("app") as HTMLElement

let snap: Snapshot = emptySnapshot()
let draft = vscode.getState()?.draft ?? ""
let panel: "none" | "slash" | "skills" | "memory" | "jobs" | "help" = "none"
let slashFilter = ""
let files: { mention: string; relative: string }[] = []
let extraBody = ""
let extraKind = ""
let jobs: Job[] = []
let memory: MemoryEntry[] = []
let permConfirm: string | undefined
let questionCustom: Record<string, string> = {}
let questionPick: Record<string, string[]> = {}
let dismissArmedUntil = 0

function emptySnapshot(): Snapshot {
  return {
    sessionId: "",
    title: "New session",
    stance: "agent",
    running: false,
    messages: [],
    live: { content: "", thinking: "", tools: [], runId: "" },
    permissions: [],
    questions: [],
    todos: [],
    chips: [],
    model: { model: "", models: [], ready: false },
    sessionModel: "",
    skills: [],
    selectedSkills: [],
    commands: [],
    status: "",
    statusError: false,
    version: "",
  }
}

window.addEventListener("message", (ev: MessageEvent<HostToWebview>) => {
  const msg = ev.data
  switch (msg.type) {
    case "snapshot":
      snap = msg.payload
      vscode.setState({ draft, sessionId: snap.sessionId })
      render()
      break
    case "transcript":
      snap.messages = msg.messages
      renderTimeline()
      break
    case "live":
      snap.live = msg.live
      renderTimeline()
      break
    case "permissions":
      snap.permissions = msg.permissions
      renderCards()
      renderStatus(snap.statusError)
      break
    case "questions":
      snap.questions = msg.questions
      renderCards()
      renderStatus(snap.statusError)
      break
    case "todos":
      snap.todos = msg.todos
      renderTimeline()
      break
    case "context":
      snap.context = msg.context
      renderFooter()
      break
    case "model":
      snap.model = msg.model
      snap.sessionModel = msg.sessionModel
      renderFooter()
      break
    case "chips":
      snap.chips = msg.chips
      renderComposer()
      break
    case "status":
      snap.status = msg.text
      snap.statusError = !!msg.error
      renderStatus(msg.error)
      break
    case "insert":
      draft = msg.text
      vscode.setState({ draft, sessionId: snap.sessionId })
      renderComposer()
      focusInput()
      break
    case "focus":
      focusInput()
      break
    case "files":
      files = msg.files
      renderComposer()
      break
    case "slashResult":
      extraKind = msg.kind
      extraBody = msg.body
      panel = "help"
      render()
      break
    case "skills":
      snap.skills = msg.skills
      snap.selectedSkills = msg.selected
      panel = "skills"
      render()
      break
    case "memory":
      memory = msg.entries
      panel = "memory"
      render()
      break
    case "jobs":
      jobs = msg.jobs
      panel = "jobs"
      render()
      break
    case "commands":
      snap.commands = msg.commands
      render()
      break
    case "journey":
      extraKind = "journey"
      extraBody = msg.body
      panel = "help"
      render()
      break
  }
})

function post(msg: WebviewToHost): void {
  vscode.postMessage(msg)
}

function render(): void {
  const keepFocus = document.activeElement?.id === "input"
  app.innerHTML = ""
  const ver = displayVersion(snap.version)
  const meta = ["ymz"]
  if (ver) {
    meta.push(ver)
  }
  meta.push(snap.title || "YunmengZe")
  app.append(el("header", "hdr", [
    el("div", "hdr-title", [text(meta.join("  ·  "))]),
    el("div", "hdr-sub", [text(snap.sessionId ? short(snap.sessionId) : "new")]),
  ]))
  const timeline = el("div", "timeline")
  timeline.id = "timeline"
  app.append(timeline)
  renderTimeline()
  const cards = el("div", "cards")
  cards.id = "cards"
  app.append(cards)
  renderCards()
  if (panel !== "none") {
    app.append(renderPanel())
  }
  const composer = el("div", "composer")
  composer.id = "composer"
  fillComposer(composer)
  app.append(composer)
  const foot = el("div", "foot")
  foot.id = "foot"
  fillFooter(foot)
  app.append(foot)
  const status = el("div", "status")
  status.id = "status"
  status.textContent = snap.status
  status.classList.toggle("err", !!snap.statusError)
  app.append(status)
  if (keepFocus) {
    focusInput()
  }
}

function displayVersion(v?: string): string {
  const s = (v || "").trim()
  if (!s || s === "0.0.1" || s === "0.0.0-dev") {
    return "dev"
  }
  return s
}

function renderStatus(error?: boolean): void {
  const n = document.getElementById("status")
  if (!n) {
    return
  }
  n.textContent = snap.status
  n.classList.toggle("err", !!error)
}

function renderTimeline(): void {
  const root = document.getElementById("timeline")
  if (!root) {
    return
  }
  const stick = nearBottom(root)
  root.innerHTML = ""
  for (const todo of snap.todos) {
    root.append(el("div", `todo ${todo.status}`, [text(`${todo.status} · ${todo.content}`)]))
  }
  const toolNames = toolNameByCallID(snap.messages)
  for (const m of snap.messages) {
    root.append(messageNode(m, toolNames))
  }
  if (snap.live.content || snap.live.thinking || snap.live.tools.length) {
    root.append(liveNode(snap.live))
  }
  if (stick) {
    root.scrollTop = root.scrollHeight
  }
}

function renderCards(): void {
  const root = document.getElementById("cards")
  if (!root) {
    return
  }
  root.innerHTML = ""
  root.classList.toggle("empty", !snap.permissions.length && !snap.questions.length)
  for (const p of snap.permissions) {
    root.append(permCard(p))
  }
  if (!snap.permissions.length) {
    for (const q of snap.questions) {
      root.append(questionCard(q))
    }
  }
}

function toolNameByCallID(messages: TranscriptMessage[]): Map<string, string> {
  const names = new Map<string, string>()
  for (const m of messages) {
    for (const tc of m.tool_calls || []) {
      if (tc.id) {
        names.set(tc.id, tc.name)
      }
    }
  }
  return names
}

function messageNode(m: TranscriptMessage, toolNames: Map<string, string>): HTMLElement {
  const wrap = el("article", `msg ${m.role}`)
  wrap.append(el("div", "msg-role", [text(roleLabel(m.role))]))
  if (m.thinking) {
    wrap.append(fold("thinking", mdLite(m.thinking)))
  }
  if (m.tool_calls?.length) {
    for (const tc of m.tool_calls) {
      wrap.append(toolCallNode(tc))
    }
  }
  if (m.role === "tool") {
    wrap.append(toolResultNode(m, toolNames.get(m.tool_call_id || "") || ""))
  } else if (m.content) {
    wrap.append(mdLite(m.content))
  }
  return wrap
}

function roleLabel(role: string): string {
  switch (role) {
    case "user":
      return "you"
    case "assistant":
      return "assistant"
    case "tool":
      return "tool"
    default:
      return role
  }
}

function toolCallNode(tc: TranscriptToolCall): HTMLElement {
  const preview = toolCallPreview(tc.name, tc.arguments || "")
  const title = preview ? `⚙ ${tc.name} · ${preview}` : `⚙ ${tc.name}`
  const body = el("div", "tool-body")
  const path = extractPath(tc.arguments || "")
  if (path) {
    const a = el("button", "link", [text(path)]) as HTMLButtonElement
    a.addEventListener("click", () => post({ type: "openPath", path }))
    body.append(a)
  }
  if (tc.arguments && tc.arguments !== "{}") {
    body.append(el("pre", "code", [text(prettyJSON(tc.arguments))]))
  }
  return fold(title, body)
}

function toolResultNode(m: TranscriptMessage, toolName: string): HTMLElement {
  const parsed = parseToolJSON(m.content)
  const err = typeof parsed?.error === "string" ? parsed.error : ""
  const name = toolName || (typeof parsed?.tool === "string" ? parsed.tool : "result")
  const label = m.tool_call_id ? `${name} ${short(m.tool_call_id)}` : name
  if (err === "orphan_tool_call" || err === "interrupted" || err === "tool_denied" || err === "tool_failed") {
    const hint = typeof parsed?.hint === "string" ? parsed.hint : ""
    const msg = typeof parsed?.message === "string" ? parsed.message : err
    const box = el("div", `tool-err ${err}`)
    box.append(el("div", "tool-err-h", [text(`${err} · ${label}`)]))
    box.append(el("div", "tool-err-b", [text(msg)]))
    if (hint) {
      box.append(el("div", "hint", [text(hint)]))
    }
    return box
  }
  const body = el("div", "tool-body")
  const path = typeof parsed?.path === "string" ? parsed.path : extractPath(m.content)
  if (path) {
    const a = el("button", "link", [text(path)]) as HTMLButtonElement
    a.addEventListener("click", () => post({ type: "openPath", path }))
    body.append(a)
  }
  body.append(el("pre", "code", [text(prettyJSON(m.content))]))
  return fold(`· ${label}`, body)
}

function liveNode(live: LiveState): HTMLElement {
  const wrap = el("article", "msg assistant live")
  wrap.append(el("div", "msg-role", [text("assistant")]))
  if (live.thinking) {
    wrap.append(fold("thinking", mdLite(live.thinking)))
  }
  for (const t of live.tools) {
    const title = t.preview ? `⚙ ${t.name} · ${t.preview}` : `⚙ ${t.name}`
    wrap.append(fold(title, mdLite(t.preview)))
  }
  if (live.content) {
    wrap.append(mdLite(live.content))
  } else {
    wrap.append(el("div", "pulse", [text("…")]))
  }
  return wrap
}

function permCard(p: Permission): HTMLElement {
  const card = el("div", "card perm")
  if (Date.now() < dismissArmedUntil) {
    card.classList.add("armed")
    card.append(el("div", "armed-h", [text("press Esc again in 3s to deny")]))
  }
  const title = p.extra_root ? "Permission · extra root" : "Permission"
  const head = el("div", "card-h")
  head.append(el("span", "", [text(title)]))
  head.append(el("span", "card-tool", [text(p.tool_name)]))
  if (p.risk) {
    head.append(el("span", "dim", [text(p.risk)]))
  }
  card.append(head)
  if (p.path) {
    const pathBtn = el("button", "link path", [text(p.path)]) as HTMLButtonElement
    pathBtn.addEventListener("click", () => post({ type: "openPath", path: p.path || "" }))
    card.append(pathBtn)
  }
  if (p.extra_root) {
    card.append(el("div", "hint", [text("outside workspace — extra root")]))
  }
  const cmd = [p.command, ...(p.command_args || [])].filter(Boolean).join(" ")
  if (cmd) {
    card.append(el("div", "card-b", [text(`$ ${cmd}`)]))
  }
  if (p.network_domain) {
    card.append(el("div", "card-b", [text(`host ${p.network_domain}`)]))
  }
  if (p.suggested_reason) {
    card.append(el("div", "hint", [text(p.suggested_reason)]))
  } else if (p.suggested_decision) {
    card.append(el("div", "hint", [text(`hint ${p.suggested_decision}`)]))
  }
  const row = el("div", "row")
  const permanentLabel = p.extra_root ? "permanent · write chat.workspace.allow" : "permanent · remember this tool"
  for (const [label, decision] of [
    ["once · this call", "allow_once"],
    ["similar · this session", "allow_similar"],
    [permanentLabel, "allow_permanent"],
    ["deny", "deny"],
  ] as const) {
    const b = el("button", decision === "deny" ? "btn danger" : "btn", [text(label)]) as HTMLButtonElement
    b.addEventListener("click", () => {
      if (decision === "allow_permanent" && permConfirm !== p.permission_id) {
        permConfirm = p.permission_id
        b.textContent = "confirm permanent"
        b.classList.add("on")
        return
      }
      permConfirm = undefined
      post({ type: "decide", permissionId: p.permission_id, decision })
    })
    row.append(b)
  }
  card.append(row)
  card.append(el("div", "card-k", [text("Esc Esc deny · /perm once|similar|permanent|deny <id>")]))
  return card
}

function questionCard(q: UserQuestion): HTMLElement {
  const card = el("div", "card q")
  card.append(el("div", "card-h", [text("question")]))
  for (const item of q.questions) {
    card.append(el("div", "q-h", [text(item.header || item.question)]))
    if (item.header) {
      card.append(el("div", "q-q", [text(item.question)]))
    }
    const picks = questionPick[item.id] || []
    if (item.options?.length) {
      const opts = el("div", "opts")
      for (const o of item.options) {
        const b = el("button", picks.includes(o.label) ? "btn on" : "btn", [text(o.label)]) as HTMLButtonElement
        if (o.description) {
          b.title = o.description
        }
        b.addEventListener("click", () => {
          const cur = new Set(questionPick[item.id] || [])
          if (item.multi_select) {
            if (cur.has(o.label)) {
              cur.delete(o.label)
            } else {
              cur.add(o.label)
            }
          } else {
            cur.clear()
            cur.add(o.label)
          }
          questionPick[item.id] = [...cur]
          render()
        })
        opts.append(b)
      }
      card.append(opts)
    }
    const custom = el("input", "inp") as HTMLInputElement
    custom.placeholder = "Type your own answer"
    custom.value = questionCustom[item.id] || ""
    custom.addEventListener("input", () => {
      questionCustom[item.id] = custom.value
    })
    card.append(custom)
  }
  const row = el("div", "row")
  const send = el("button", "btn primary", [text("answer")]) as HTMLButtonElement
  send.addEventListener("click", () => {
    const answers: Record<string, string[]> = {}
    for (const item of q.questions) {
      const custom = (questionCustom[item.id] || "").trim()
      answers[item.id] = custom ? [custom] : questionPick[item.id] || []
    }
    post({ type: "answer", questionId: q.question_id, answers })
    questionCustom = {}
    questionPick = {}
  })
  const dismiss = el("button", "btn", [text("dismiss")]) as HTMLButtonElement
  dismiss.addEventListener("click", () => post({ type: "dismiss", questionId: q.question_id }))
  row.append(send, dismiss)
  card.append(row)
  return card
}

function renderPanel(): HTMLElement {
  const box = el("div", "panel")
  const close = el("button", "btn ghost", [text("close")]) as HTMLButtonElement
  close.addEventListener("click", () => {
    panel = "none"
    render()
  })
  box.append(el("div", "panel-h", [text(panel), close]))
  if (panel === "slash") {
    box.append(slashList())
  } else if (panel === "skills") {
    for (const s of snap.skills) {
      const on = snap.selectedSkills.includes(s.id)
      const row = el("div", "row line")
      const t = el("button", on ? "btn on" : "btn", [text(`${on ? "●" : "○"} ${s.id}`)]) as HTMLButtonElement
      t.addEventListener("click", () => post({ type: "toggleSkill", id: s.id }))
      row.append(t, el("span", "dim", [text(s.description || s.name)]))
      if (s.draft) {
        const a = el("button", "btn", [text("apply")]) as HTMLButtonElement
        a.addEventListener("click", () => post({ type: "skillAction", action: "apply", id: s.id }))
        const r = el("button", "btn", [text("reject")]) as HTMLButtonElement
        r.addEventListener("click", () => post({ type: "skillAction", action: "reject", id: s.id }))
        row.append(a, r)
      }
      box.append(row)
    }
  } else if (panel === "memory") {
    for (const e of memory) {
      const row = el("div", "row line")
      row.append(el("div", "grow", [text(`${e.kind || e.source}: ${e.content}`)]))
      const f = el("button", "btn", [text("forget")]) as HTMLButtonElement
      f.addEventListener("click", () => post({ type: "memoryAction", action: "forget", id: e.entry_id }))
      const p = el("button", "btn", [text("promote")]) as HTMLButtonElement
      p.addEventListener("click", () => post({ type: "memoryAction", action: "promote", id: e.entry_id }))
      row.append(f, p)
      box.append(row)
    }
    const refresh = el("button", "btn", [text("refresh inject")]) as HTMLButtonElement
    refresh.addEventListener("click", () => post({ type: "memoryAction", action: "refresh" }))
    box.append(refresh)
  } else if (panel === "jobs") {
    for (const j of jobs) {
      const row = el("div", "row line")
      row.append(el("div", "grow", [text(`${j.status} · ${j.interval_seconds}s · ${j.task_objective}`)]))
      for (const a of ["pause", "resume", "cancel"] as const) {
        const b = el("button", "btn", [text(a)]) as HTMLButtonElement
        b.addEventListener("click", () => post({ type: "jobAction", id: j.id, action: a }))
        row.append(b)
      }
      box.append(row)
    }
  } else if (panel === "help") {
    box.append(el("pre", "pre", [text(extraBody || extraKind)]))
  }
  return box
}

function slashList(): HTMLElement {
  const wrap = el("div", "slash")
  const items = [
    ...builtinItems(),
    ...snap.commands.map((c) => ({ name: c.id, desc: c.description || "command", cmd: true as const })),
    ...snap.skills.map((s) => ({ name: s.id, desc: s.name || "skill", skill: true as const })),
  ].filter((i) => !slashFilter || i.name.toLowerCase().includes(slashFilter.toLowerCase()))
  for (const i of items.slice(0, 30)) {
    const b = el("button", "slash-item", [el("b", "", [text("/" + i.name)]), el("span", "dim", [text(i.desc)])]) as HTMLButtonElement
    b.addEventListener("click", () => {
      draft = `/${i.name} `
      panel = "none"
      render()
      focusInput()
    })
    wrap.append(b)
  }
  return wrap
}

function builtinItems(): { name: string; desc: string }[] {
  return [
    { name: "new", desc: "new session tab" },
    { name: "compact", desc: "summarize history" },
    { name: "undo", desc: "rewind last file write" },
    { name: "edit", desc: "retract last turn" },
    { name: "editundo", desc: "retract + rewind files" },
    { name: "retry", desc: "resubmit last user message" },
    { name: "cancel", desc: "stop running turn" },
    { name: "model", desc: "session or main model" },
    { name: "status", desc: "daemon / model / mcp" },
    { name: "skills", desc: "list / apply / reject" },
    { name: "memory", desc: "session memory" },
    { name: "cron", desc: "list or create job" },
    { name: "perm", desc: "once|similar|permanent|deny <id>" },
    { name: "help", desc: "this list" },
  ]
}

function renderComposer(): void {
  const box = document.getElementById("composer")
  if (!box) {
    return
  }
  fillComposer(box)
}

function fillComposer(box: HTMLElement): void {
  box.className = "composer"
  box.innerHTML = ""
  box.append(stampRow())
  if (snap.chips.length) {
    const chips = el("div", "chips")
    for (const c of snap.chips) {
      chips.append(chipNode(c))
    }
    box.append(chips)
  }
  if (files.length && draft.includes("@")) {
    const sug = el("div", "suggest")
    for (const f of files) {
      const b = el("button", "slash-item", [text(f.mention)]) as HTMLButtonElement
      b.addEventListener("click", () => {
        draft = draft.replace(/@([^\s]*)$/, f.mention + " ")
        files = []
        vscode.setState({ draft, sessionId: snap.sessionId })
        renderComposer()
        focusInput()
      })
      sug.append(b)
    }
    box.append(sug)
  }
  const ta = el("textarea", "ta") as HTMLTextAreaElement
  ta.id = "input"
  ta.rows = 3
  ta.placeholder = snap.running ? "Steer the next step…" : "Message YunmengZe — drop files, or type /"
  ta.value = draft
  ta.addEventListener("input", () => {
    draft = ta.value
    vscode.setState({ draft, sessionId: snap.sessionId })
    if (draft.startsWith("/") && !draft.includes("\n")) {
      slashFilter = draft.slice(1)
      if (panel !== "slash") {
        panel = "slash"
        render()
        const again = document.getElementById("input") as HTMLTextAreaElement | null
        if (again) {
          again.value = draft
          again.focus()
        }
      } else {
        const list = document.querySelector(".slash")
        if (list) {
          list.replaceWith(slashList())
        }
      }
    } else if (panel === "slash") {
      panel = "none"
      render()
    }
    const at = draft.match(/@([^\s]*)$/)
    if (at) {
      post({ type: "searchFiles", query: at[1] })
    }
  })
  ta.addEventListener("keydown", (e) => {
    if (e.key === "Tab") {
      e.preventDefault()
      const next = cycleStance(snap.stance, e.shiftKey ? -1 : 1)
      snap.stance = next
      post({ type: "stance", stance: next })
      renderFooter()
      const stamp = document.getElementById("stamp")
      if (stamp) {
        stamp.replaceWith(stampRow())
      }
      return
    }
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault()
      submit()
    }
    if (e.key === "Escape") {
      if (panel !== "none") {
        panel = "none"
        dismissArmedUntil = 0
        render()
        focusInput()
        return
      }
      if (snap.permissions.length) {
        const now = Date.now()
        if (now < dismissArmedUntil) {
          dismissArmedUntil = 0
          post({ type: "decide", permissionId: snap.permissions[0].permission_id, decision: "deny" })
          return
        }
        dismissArmedUntil = now + 3000
        snap.status = "press Esc again in 3s to deny"
        snap.statusError = true
        renderCards()
        renderStatus(true)
        return
      }
      if (snap.questions.length) {
        const now = Date.now()
        if (now < dismissArmedUntil) {
          dismissArmedUntil = 0
          post({ type: "dismiss", questionId: snap.questions[0].question_id })
          return
        }
        dismissArmedUntil = now + 3000
        snap.status = "press Esc again in 3s to dismiss"
        renderStatus()
      }
    }
  })
  box.ondragover = (e) => {
    e.preventDefault()
    box.classList.add("drag")
  }
  box.ondragleave = () => box.classList.remove("drag")
  box.ondrop = (e) => {
    e.preventDefault()
    box.classList.remove("drag")
    const uris: string[] = []
    const paths: string[] = []
    if (e.dataTransfer) {
      for (const item of Array.from(e.dataTransfer.items || [])) {
        if (item.kind === "string" && item.type === "text/uri-list") {
          item.getAsString((s) => uris.push(...s.split("\n").map((x) => x.trim()).filter(Boolean)))
        }
      }
      for (const f of Array.from(e.dataTransfer.files || [])) {
        const anyF = f as File & { path?: string }
        if (anyF.path) {
          paths.push(anyF.path)
        }
      }
    }
    window.setTimeout(() => post({ type: "drop", uris, paths }), 0)
  }
  box.append(ta)
}

function chipNode(c: Chip): HTMLElement {
  const n = el("span", `chip ${c.kind}${c.outside ? " out" : ""}`)
  n.append(text(c.mention))
  const x = el("button", "x", [text("×")]) as HTMLButtonElement
  x.addEventListener("click", () => post({ type: "removeChip", id: c.id }))
  n.append(x)
  return n
}

function renderFooter(): void {
  const foot = document.getElementById("foot")
  if (!foot) {
    return
  }
  fillFooter(foot)
}

function stampRow(): HTMLElement {
  const row = el("div", "stamp")
  row.id = "stamp"
  const chip = el("span", `stamp-chip ${snap.stance}`, [text(snap.stance.toUpperCase())])
  row.append(chip)
  row.append(el("span", "stamp-model", [text(snap.sessionModel || snap.model.model || "—")]))
  row.append(el("span", "stamp-rule", []))
  return row
}

function fillFooter(foot: HTMLElement): void {
  foot.className = "foot"
  foot.innerHTML = ""
  const stances: Stance[] = ["agent", "plan", "auto"]
  const seg = el("div", "seg")
  for (const s of stances) {
    const b = el("button", snap.stance === s ? `seg-on ${s}` : s, [text(s)]) as HTMLButtonElement
    b.addEventListener("click", () => post({ type: "stance", stance: s }))
    seg.append(b)
  }
  foot.append(seg)
  const modelBtn = el("button", "btn ghost", [text(snap.sessionModel || snap.model.model || "model")]) as HTMLButtonElement
  modelBtn.addEventListener("click", () => {
    const next = window.prompt("Session model (provider/model). Prefix with main to switch global.", snap.sessionModel || snap.model.model)
    if (next == null) {
      return
    }
    const t = next.trim()
    if (t.startsWith("main ")) {
      post({ type: "setMainModel", model: t.slice(5).trim() })
    } else {
      post({ type: "preferModel", model: t })
    }
  })
  foot.append(modelBtn)
  if (snap.context && snap.context.context_window) {
    const pct = Math.min(100, Math.round((snap.context.pressure || 0) * 100))
    foot.append(el("span", "dim", [text(`ctx ${pct}%`)]))
  }
  const attach = el("button", "btn ghost", [text("@")]) as HTMLButtonElement
  attach.addEventListener("click", () => post({ type: "pickFiles" }))
  foot.append(attach)
  if (snap.running) {
    const stop = el("button", "btn danger", [text("Stop")]) as HTMLButtonElement
    stop.addEventListener("click", () => post({ type: "stop" }))
    foot.append(stop)
  }
}

function submit(): void {
  const t = draft
  if (t.trim().startsWith("/")) {
    post({ type: "slash", line: t })
  } else {
    post({ type: "send", text: t })
  }
  draft = ""
  vscode.setState({ draft, sessionId: snap.sessionId })
  const ta = document.getElementById("input") as HTMLTextAreaElement | null
  if (ta) {
    ta.value = ""
  }
}

function focusInput(): void {
  const ta = document.getElementById("input") as HTMLTextAreaElement | null
  ta?.focus()
}

function fold(title: string, body: Node): HTMLElement {
  const d = document.createElement("details")
  d.className = "fold"
  const s = document.createElement("summary")
  s.textContent = title
  d.append(s, body instanceof HTMLElement ? body : el("div", "", [body]))
  return d
}

function mdLite(src: string): HTMLElement {
  const pre = el("div", "md")
  const parts = src.split(/```/)
  if (parts.length === 1) {
    pre.append(inline(src))
    return pre
  }
  for (let i = 0; i < parts.length; i++) {
    if (i % 2 === 0) {
      pre.append(inline(parts[i]))
    } else {
      const nl = parts[i].indexOf("\n")
      const code = nl >= 0 ? parts[i].slice(nl + 1) : parts[i]
      const block = el("pre", "code")
      block.textContent = code
      pre.append(block)
    }
  }
  return pre
}

function parseToolJSON(src: string): Record<string, unknown> | undefined {
  try {
    const j = JSON.parse(src) as unknown
    if (j && typeof j === "object" && !Array.isArray(j)) {
      return j as Record<string, unknown>
    }
  } catch {
    /* ignore */
  }
  return undefined
}

function prettyJSON(src: string): string {
  try {
    return JSON.stringify(JSON.parse(src), null, 2)
  } catch {
    return src
  }
}

function toolCallPreview(name: string, argumentsJSON: string): string {
  const args = parseToolJSON(argumentsJSON) || {}
  const field = (...keys: string[]): string => {
    for (const k of keys) {
      const v = args[k]
      if (typeof v === "string" && v.trim()) {
        return v.trim()
      }
    }
    return ""
  }
  if (name.startsWith("fs_")) {
    return basename(field("path", "file", "filepath", "target", "dest"))
  }
  if (name.startsWith("process_")) {
    const cmd = field("command", "cmd")
    if (cmd) {
      return truncate(cmd, 72)
    }
    const argv = args.argv
    if (Array.isArray(argv)) {
      return truncate(argv.map(String).join(" "), 72)
    }
  }
  if (name.startsWith("git_")) {
    return basename(field("path", "repo", "repository")) || field("command")
  }
  return field("path", "url", "uri", "command", "query")
}

function basename(p: string): string {
  if (!p) {
    return ""
  }
  const n = p.replace(/\\/g, "/").split("/").filter(Boolean).pop()
  return n || p
}

function truncate(s: string, n: number): string {
  const runes = Array.from(s)
  if (runes.length <= n) {
    return s
  }
  return `${runes.slice(0, n).join("")}…`
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
}

function inline(src: string): HTMLElement {
  const wrap = el("div", "prose")
  const lines = src.split("\n")
  let list: HTMLElement | undefined
  const flushList = () => {
    if (list) {
      wrap.append(list)
      list = undefined
    }
  }
  for (const line of lines) {
    const heading = /^(#{1,3})\s+(.*)$/.exec(line)
    if (heading) {
      flushList()
      const h = el(`h${heading[1].length}` as "h1" | "h2" | "h3", "", [])
      h.innerHTML = inlineHTML(heading[2])
      wrap.append(h)
      continue
    }
    const bullet = /^[-*]\s+(.*)$/.exec(line)
    if (bullet) {
      if (!list) {
        list = el("ul", "")
      }
      const li = el("li", "")
      li.innerHTML = inlineHTML(bullet[1])
      list.append(li)
      continue
    }
    flushList()
    if (!line.trim()) {
      wrap.append(el("div", "gap"))
      continue
    }
    const p = el("div", "")
    p.innerHTML = inlineHTML(line)
    wrap.append(p)
  }
  flushList()
  wrap.querySelectorAll("button.at").forEach((b) => {
    b.addEventListener("click", () => post({ type: "openPath", path: (b as HTMLElement).dataset.p || "" }))
  })
  return wrap
}

function inlineHTML(src: string): string {
  return escapeHtml(src)
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/@([A-Za-z0-9_./@+-]+(?:#L\d+(?:-\d+)?)?)/g, '<button class="at" data-p="$1">@$1</button>')
}

function extractPath(args: string): string {
  try {
    const j = JSON.parse(args) as { path?: string }
    return typeof j.path === "string" ? j.path : ""
  } catch {
    const m = args.match(/"path"\s*:\s*"([^"]+)"/)
    return m ? m[1] : ""
  }
}

function nearBottom(n: HTMLElement): boolean {
  return n.scrollHeight - n.scrollTop - n.clientHeight < 80
}

function short(id: string): string {
  return id.length <= 12 ? id : id.slice(0, 10)
}

function el(tag: string, cls: string, kids: Array<Node | string> = []): HTMLElement {
  const n = document.createElement(tag)
  if (cls) {
    n.className = cls
  }
  for (const k of kids) {
    n.append(typeof k === "string" ? document.createTextNode(k) : k)
  }
  return n
}

function text(s: string): Text {
  return document.createTextNode(s)
}

post({ type: "ready" })
render()
