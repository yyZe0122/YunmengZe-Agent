import * as vscode from "vscode"
import { ensureGateway, openTuiTerminal } from "../daemon"
import { Gateway, GatewayError } from "../gateway"
import {
  composeMessage,
  executionModeForStance,
  expandChatCommandTemplate,
  imageLike,
  isBuiltinSlash,
  normalizeDecision,
  parseCronEvery,
  parseSlash,
  taskTitle,
  videoLike,
  withLineRange,
  workspaceRelative,
} from "../paths"
import { SessionTree } from "../sessions/tree"
import type { ChatCommand, Chip, LiveState, ModelConfig, Session, Stance, Task } from "../types"
import type { HostToWebview, Snapshot, WebviewToHost } from "./protocol"

const VIEW_TYPE = "ymz.chat"
let draftSeq = 0

export class ChatHost {
  readonly tree = new SessionTree()
  private readonly panels = new Map<string, ChatPanel>()
  private active?: ChatPanel
  private gw?: Gateway
  private model: ModelConfig = { model: "", models: [], ready: false }
  private commands: ChatCommand[] = []
  private listTimer: ReturnType<typeof setInterval> | undefined

  constructor(readonly context: vscode.ExtensionContext) {}

  activate(): void {
    this.context.subscriptions.push(
      vscode.window.registerWebviewPanelSerializer(VIEW_TYPE, {
        deserializeWebviewPanel: async (panel, state) => {
          const sessionId = ((state as { sessionId?: string } | undefined)?.sessionId ?? "").trim()
          const chat = this.attachPanel(panel, sessionId, sessionId ? shortTitle(sessionId) : "New session")
          await chat.load()
        },
      }),
      vscode.window.registerTreeDataProvider("ymz.sessions", this.tree),
    )
  }

  dispose(): void {
    if (this.listTimer) {
      clearInterval(this.listTimer)
    }
    for (const p of this.panels.values()) {
      p.dispose()
    }
  }

  useTerminal(): boolean {
    return vscode.workspace.getConfiguration("ymz").get<boolean>("useTerminal") === true
  }

  async focusInput(): Promise<void> {
    if (this.useTerminal()) {
      openTuiTerminal({ reuse: true })
      return
    }
    if (this.active && this.active.visible()) {
      this.active.post({ type: "focus" })
      this.active.reveal()
      return
    }
    await this.openNew()
  }

  async openNew(): Promise<void> {
    if (this.useTerminal()) {
      openTuiTerminal({ reuse: false })
      return
    }
    const column = this.preferredColumn()
    const panel = vscode.window.createWebviewPanel(VIEW_TYPE, "YunmengZe", column, webviewOptions(this.context))
    const chat = this.attachPanel(panel, "", "New session")
    await chat.load()
  }

  async openSession(sessionId: string): Promise<void> {
    const existing = this.panels.get(sessionId)
    if (existing) {
      existing.reveal()
      existing.post({ type: "focus" })
      return
    }
    const column = this.preferredColumn()
    const panel = vscode.window.createWebviewPanel(VIEW_TYPE, shortTitle(sessionId), column, webviewOptions(this.context))
    const chat = this.attachPanel(panel, sessionId, shortTitle(sessionId))
    await chat.load()
  }

  async insertAtMention(): Promise<void> {
    const editor = vscode.window.activeTextEditor
    if (!editor || editor.document.uri.scheme !== "file") {
      return
    }
    const ref = fileRefFromEditor(editor)
    if (!this.active) {
      await this.openNew()
    }
    this.active?.addChipFromRef(ref)
  }

  async addUri(uri: vscode.Uri): Promise<void> {
    if (uri.scheme !== "file") {
      return
    }
    if (!this.active) {
      await this.openNew()
    }
    this.active?.addAbs(uri.fsPath)
  }

  private preferredColumn(): vscode.ViewColumn {
    const loc = vscode.workspace.getConfiguration("ymz").get<string>("preferredLocation")
    if (loc === "sidebar") {
      return vscode.ViewColumn.Beside
    }
    return vscode.ViewColumn.Active
  }

  private attachPanel(panel: vscode.WebviewPanel, sessionId: string, title: string): ChatPanel {
    const key = sessionId || `draft:${++draftSeq}`
    const chat = new ChatPanel(this, panel, key, sessionId, title)
    this.panels.set(key, chat)
    this.active = chat
    panel.onDidChangeViewState((e) => {
      if (e.webviewPanel.active) {
        this.active = chat
        chat.onBecameActive()
      }
    })
    panel.onDidDispose(() => {
      this.panels.delete(chat.key)
      if (this.active === chat) {
        this.active = [...this.panels.values()].at(-1)
      }
    })
    return chat
  }

  rekey(panel: ChatPanel, sessionId: string): void {
    if (panel.key === sessionId) {
      return
    }
    this.panels.delete(panel.key)
    panel.key = sessionId
    this.panels.set(sessionId, panel)
  }

  async gateway(): Promise<Gateway> {
    if (!this.gw) {
      this.gw = await ensureGateway()
      try {
        this.model = await this.gw.modelConfig()
        this.commands = await this.gw.listCommands()
      } catch {
        /* snapshot later */
      }
      this.startListTimer()
      void this.refreshSessions()
    }
    return this.gw
  }

  private startListTimer(): void {
    if (this.listTimer) {
      return
    }
    this.listTimer = setInterval(() => void this.refreshSessions(), 8000)
  }

  currentModel(): ModelConfig {
    return this.model
  }

  currentCommands(): ChatCommand[] {
    return this.commands
  }

  setModel(m: ModelConfig): void {
    this.model = m
  }

  async refreshSessions(): Promise<void> {
    try {
      const gw = await this.gateway()
      const sessions = await gw.listSessions(80)
      this.tree.refresh(sessions)
    } catch (err) {
      this.tree.refresh([], err instanceof Error ? err.message : String(err))
    }
  }

  html(webview: vscode.Webview): string {
    const script = webview.asWebviewUri(vscode.Uri.joinPath(this.context.extensionUri, "dist", "webview.js"))
    const style = webview.asWebviewUri(vscode.Uri.joinPath(this.context.extensionUri, "dist", "webview.css"))
    const nonce = String(Date.now())
    return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src ${webview.cspSource} data:; style-src ${webview.cspSource} 'unsafe-inline'; script-src 'nonce-${nonce}';">
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="stylesheet" href="${style}">
<title>YunmengZe</title>
</head>
<body>
<div id="app"></div>
<script nonce="${nonce}" src="${script}"></script>
</body>
</html>`
  }
}

function webviewOptions(context: vscode.ExtensionContext): vscode.WebviewPanelOptions & vscode.WebviewOptions {
  return {
    enableScripts: true,
    retainContextWhenHidden: true,
    localResourceRoots: [vscode.Uri.joinPath(context.extensionUri, "dist"), vscode.Uri.joinPath(context.extensionUri, "images")],
  }
}

class ChatPanel {
  key: string
  private sessionId: string
  private title: string
  private stance: Stance = "agent"
  private sessionModel = ""
  private task?: Task
  private messages: Snapshot["messages"] = []
  private live: LiveState = { content: "", thinking: "", tools: [], runId: "" }
  private permissions: Snapshot["permissions"] = []
  private questions: Snapshot["questions"] = []
  private todos: Snapshot["todos"] = []
  private chips: Chip[] = []
  private skills: Snapshot["skills"] = []
  private selectedSkills: string[] = []
  private contextInfo?: Snapshot["context"]
  private status = ""
  private running = false
  private eventsAbort?: AbortController
  private modelAbort?: AbortController
  private sseAfter = 0
  private pendingDot: "none" | "permission" | "done" = "none"

  constructor(
    private readonly host: ChatHost,
    private readonly panel: vscode.WebviewPanel,
    key: string,
    sessionId: string,
    title: string,
  ) {
    this.key = key
    this.sessionId = sessionId
    this.title = title
    this.panel.webview.html = host.html(this.panel.webview)
    this.panel.iconPath = {
      light: vscode.Uri.joinPath(host.context.extensionUri, "images", "button-dark.svg"),
      dark: vscode.Uri.joinPath(host.context.extensionUri, "images", "button-light.svg"),
    }
    this.panel.onDidDispose(() => this.stopStreams())
    this.panel.webview.onDidReceiveMessage((msg: WebviewToHost) => {
      void this.onMessage(msg)
    })
  }

  onBecameActive(): void {
    if (this.pendingDot === "done") {
      this.pendingDot = "none"
      this.panel.title = this.tabTitle()
    }
  }

  visible(): boolean {
    return this.panel.visible
  }

  reveal(): void {
    this.panel.reveal(undefined, false)
  }

  dispose(): void {
    this.stopStreams()
    this.panel.dispose()
  }

  post(msg: HostToWebview): void {
    void this.panel.webview.postMessage(msg)
  }

  addChipFromRef(mention: { mention: string; abs: string; outside: boolean }): void {
    this.addChip(mention.mention, mention.abs, mention.outside)
    this.reveal()
    this.post({ type: "focus" })
  }

  addAbs(abs: string): void {
    const root = workspaceRoot()
    const ref = workspaceRelative(abs, root)
    this.addChip(ref.mention, ref.abs, ref.outside)
    this.reveal()
    this.post({ type: "chips", chips: this.chips })
    this.post({ type: "focus" })
  }

  async load(): Promise<void> {
    try {
      const gw = await this.host.gateway()
      if (this.sessionId) {
        const session = await gw.getSession(this.sessionId)
        this.applySession(session)
        this.messages = await gw.sessionMessages(this.sessionId)
        this.todos = await gw.sessionTodos(this.sessionId).catch(() => [])
        this.permissions = await gw.listPermissions(this.sessionId).catch(() => [])
        this.questions = await gw.listQuestions(this.sessionId).catch(() => [])
        this.contextInfo = await gw.sessionContext(this.sessionId).catch(() => undefined)
        if (session.latest_task_id) {
          this.task = await gw.getTask(session.latest_task_id).catch(() => undefined)
          this.running = this.task?.state === "running"
        }
        this.startStreams()
      }
      this.skills = await gw.listSkills().catch(() => [])
      this.status = this.sessionId ? "" : "new session"
      this.pushSnapshot()
    } catch (err) {
      this.status = errText(err)
      this.pushSnapshot()
    }
  }

  private applySession(session: Session): void {
    this.sessionId = session.session_id
    this.title = session.title || shortTitle(session.session_id)
    this.panel.title = this.tabTitle()
    this.sessionModel = session.preferred_model || ""
    const st = session.permission_stance
    if (st === "plan" || st === "auto" || st === "agent") {
      this.stance = st
    }
    this.host.rekey(this, session.session_id)
  }

  private tabTitle(): string {
    const base = this.title || "YunmengZe"
    if (this.pendingDot === "permission") {
      return `● ${base}`
    }
    if (this.pendingDot === "done" && !this.panel.active) {
      return `○ ${base}`
    }
    return base
  }

  private snapshot(): Snapshot {
    return {
      sessionId: this.sessionId,
      title: this.title,
      stance: this.stance,
      running: this.running,
      task: this.task,
      messages: this.messages,
      live: this.live,
      permissions: this.permissions,
      questions: this.questions,
      todos: this.todos,
      chips: this.chips,
      model: this.host.currentModel(),
      sessionModel: this.sessionModel,
      context: this.contextInfo,
      skills: this.skills,
      selectedSkills: this.selectedSkills,
      commands: this.host.currentCommands(),
      status: this.status,
    }
  }

  private pushSnapshot(): void {
    this.panel.title = this.tabTitle()
    this.post({ type: "snapshot", payload: this.snapshot() })
  }

  private addChip(mention: string, abs: string, outside: boolean): void {
    if (this.chips.some((c) => c.abs === abs && c.mention === mention)) {
      return
    }
    const kind = imageLike(abs) ? "image" : videoLike(abs) ? "video" : "file"
    this.chips.push({ id: `${Date.now()}-${this.chips.length}`, mention, abs, outside, kind })
    this.post({ type: "chips", chips: this.chips })
  }

  private async onMessage(msg: WebviewToHost): Promise<void> {
    try {
      switch (msg.type) {
        case "ready":
          this.pushSnapshot()
          if (!this.messages.length && this.sessionId) {
            await this.load()
          }
          return
        case "send":
          await this.send(msg.text)
          return
        case "stop":
          await this.stop()
          return
        case "stance":
          await this.setStance(msg.stance)
          return
        case "preferModel":
          await this.preferModel(msg.model)
          return
        case "setMainModel":
          await this.setMainModel(msg.model)
          return
        case "decide":
          await this.decide(msg.permissionId, msg.decision)
          return
        case "answer":
          await this.answer(msg.questionId, msg.answers)
          return
        case "dismiss":
          await this.dismiss(msg.questionId)
          return
        case "slash":
          await this.slash(msg.line)
          return
        case "compact":
          await this.doCompact(msg.focus)
          return
        case "undo":
          await this.doUndo()
          return
        case "edit":
          await this.doEdit(msg.rewindFiles)
          return
        case "retry":
          await this.retry()
          return
        case "newSession":
          await this.host.openNew()
          return
        case "pickFiles":
          await this.pickFiles()
          return
        case "searchFiles":
          await this.searchFiles(msg.query)
          return
        case "drop":
          this.takeDrops(msg.uris, msg.paths)
          return
        case "removeChip":
          this.chips = this.chips.filter((c) => c.id !== msg.id)
          this.post({ type: "chips", chips: this.chips })
          return
        case "openPath":
          await openWorkspacePath(msg.path)
          return
        case "toggleSkill":
          this.toggleSkill(msg.id)
          return
        case "skillAction":
          await this.skillAction(msg.action, msg.id)
          return
        case "memoryAction":
          await this.memoryAction(msg.action, msg.id)
          return
        case "createJob":
          await this.createJob(msg.intervalSeconds, msg.objective)
          return
        case "jobAction":
          await this.jobAction(msg.id, msg.action)
          return
      }
    } catch (err) {
      this.status = errText(err)
      this.post({ type: "status", text: this.status, error: true })
    }
  }

  private async send(text: string): Promise<void> {
    const objective = composeMessage(text, this.chips.map((c) => c.mention)).trim()
    if (!objective) {
      return
    }
    this.chips = []
    this.post({ type: "chips", chips: this.chips })
    const gw = await this.host.gateway()
    if (this.running && this.sessionId) {
      try {
        await gw.steer(this.sessionId, objective)
        this.status = "steering"
        this.pushSnapshot()
        return
      } catch (err) {
        if (!(err instanceof GatewayError && err.status === 409)) {
          throw err
        }
      }
    }
    const workspace = workspaceRoot()
    if (!workspace) {
      throw new Error("Open a folder so the session has a workspace root.")
    }
    const submitted = await gw.submit({
      session_id: this.sessionId || undefined,
      title: taskTitle(objective),
      objective,
      execution_mode: executionModeForStance(this.stance),
      workspace,
      permission_stance: this.stance,
      preferred_model: this.sessionModel || undefined,
      skill_ids: this.selectedSkills.length ? this.selectedSkills : undefined,
      interactive: true,
    })
    this.task = submitted.task
    this.running = submitted.task.state === "running" || submitted.task.state === "created"
    const sid = submitted.task.session_id || this.sessionId
    if (sid) {
      this.sessionId = sid
      this.host.rekey(this, sid)
      const session = await gw.getSession(sid).catch(() => undefined)
      if (session) {
        this.applySession(session)
      }
    }
    this.live = { content: "", thinking: "", tools: [], runId: submitted.run_id || "" }
    this.status = "running"
    this.startStreams()
    this.messages = this.sessionId ? await gw.sessionMessages(this.sessionId) : this.messages
    this.pushSnapshot()
    void this.host.refreshSessions()
  }

  private async stop(): Promise<void> {
    if (!this.task?.task_id) {
      return
    }
    const gw = await this.host.gateway()
    const current = await gw.getTask(this.task.task_id)
    this.task = await gw.controlTask(current.task_id, "cancel", current.version, "vscode stop")
    this.running = false
    this.status = "cancelled"
    this.resetLive()
    this.pushSnapshot()
  }

  private async setStance(stance: Stance): Promise<void> {
    this.stance = stance
    if (this.sessionId) {
      const gw = await this.host.gateway()
      await gw.patchSession(this.sessionId, { permission_stance: stance })
    }
    this.pushSnapshot()
  }

  private async preferModel(model: string): Promise<void> {
    this.sessionModel = model
    if (this.sessionId) {
      const gw = await this.host.gateway()
      await gw.patchSession(this.sessionId, { preferred_model: model })
    }
    this.pushSnapshot()
  }

  private async setMainModel(model: string): Promise<void> {
    const gw = await this.host.gateway()
    const cfg = await gw.setModelConfig(model)
    this.host.setModel(cfg)
    this.pushSnapshot()
  }

  private async decide(id: string, decision: string): Promise<void> {
    const norm = normalizeDecision(decision)
    if (!norm) {
      throw new Error("decision must be once / similar / permanent / deny")
    }
    const gw = await this.host.gateway()
    await gw.decidePermission(id, norm, norm === "allow_permanent")
    this.permissions = this.sessionId ? await gw.listPermissions(this.sessionId) : []
    if (!this.permissions.length) {
      this.pendingDot = this.running ? "none" : "done"
    }
    this.pushSnapshot()
  }

  private async answer(id: string, answers: Record<string, string[]>): Promise<void> {
    const gw = await this.host.gateway()
    await gw.answerQuestion(id, answers)
    this.questions = this.sessionId ? await gw.listQuestions(this.sessionId) : []
    this.pushSnapshot()
  }

  private async dismiss(id: string): Promise<void> {
    const gw = await this.host.gateway()
    await gw.dismissQuestion(id)
    this.questions = this.sessionId ? await gw.listQuestions(this.sessionId) : []
    this.pushSnapshot()
  }

  private async slash(line: string): Promise<void> {
    const parsed = parseSlash(line)
    if (!parsed) {
      await this.send(line)
      return
    }
    const { name, arg } = parsed
    const lower = name.toLowerCase()
    if (!isBuiltinSlash(lower)) {
      const cmd = this.host.currentCommands().find((c) => c.id.toLowerCase() === lower)
      if (cmd) {
        await this.send(expandChatCommandTemplate(cmd.template, arg))
        return
      }
      const skill = this.skills.find((s) => s.id.toLowerCase() === lower)
      if (skill) {
        if (!this.selectedSkills.includes(skill.id)) {
          this.selectedSkills = [...this.selectedSkills, skill.id]
        }
        if (arg) {
          await this.send(arg)
        } else {
          this.post({ type: "skills", skills: this.skills, selected: this.selectedSkills })
          this.status = `skill ${skill.id} for next submit`
          this.post({ type: "status", text: this.status })
        }
        return
      }
      throw new Error(`unknown command /${name}`)
    }
    switch (lower) {
      case "new":
        await this.host.openNew()
        return
      case "compact":
        await this.doCompact(arg)
        return
      case "undo":
        await this.doUndo()
        return
      case "edit":
        await this.doEdit(false)
        return
      case "editundo":
        await this.doEdit(true)
        return
      case "retry":
        await this.retry()
        return
      case "cancel":
        await this.stop()
        return
      case "pause":
      case "resume":
        await this.taskAction(lower, arg)
        return
      case "model":
        await this.modelSlash(arg)
        return
      case "status":
        await this.showStatus()
        return
      case "skills":
        await this.showSkills(arg)
        return
      case "memory":
        await this.showMemory(arg)
        return
      case "refresh-memory":
        await this.memoryAction("refresh")
        return
      case "journey":
        await this.showJourney(arg)
        return
      case "cron":
        await this.cronSlash(arg)
        return
      case "help":
        this.post({
          type: "slashResult",
          kind: "help",
          body: "Built-ins: /new /compact /undo /edit /editundo /retry /cancel /model /status /skills /memory /cron /perm\nDrag files onto the composer for @path chips.",
        })
        return
      case "perm":
        this.post({ type: "permissions", permissions: this.permissions })
        return
      case "sessions":
      case "back":
      case "tasks":
      case "theme":
      case "expand":
      case "quit":
        this.post({ type: "status", text: `/${lower} is TUI-only; use the sessions list or chat controls.` })
        return
      default:
        throw new Error(`unknown command /${name}`)
    }
  }

  private async doCompact(focus: string): Promise<void> {
    this.requireSession()
    const gw = await this.host.gateway()
    const r = await gw.compact(this.sessionId, focus)
    this.status = r.summary ? `compacted · ${r.summary.slice(0, 80)}` : "compacted"
    this.messages = await gw.sessionMessages(this.sessionId)
    this.pushSnapshot()
  }

  private async doUndo(): Promise<void> {
    this.requireSession()
    const gw = await this.host.gateway()
    const r = await gw.rewind(this.sessionId)
    this.status = r.path ? `rewound ${r.path}` : "rewound"
    this.post({ type: "status", text: this.status })
  }

  private async doEdit(rewindFiles: boolean): Promise<void> {
    this.requireSession()
    const gw = await this.host.gateway()
    const r = await gw.retract(this.sessionId, rewindFiles)
    this.messages = await gw.sessionMessages(this.sessionId)
    this.status = rewindFiles ? "editundo" : "edit"
    this.pushSnapshot()
    if (r.user_text) {
      this.post({ type: "insert", text: r.user_text })
    }
  }

  private async retry(): Promise<void> {
    for (let i = this.messages.length - 1; i >= 0; i--) {
      if (this.messages[i].role === "user") {
        await this.send(this.messages[i].content)
        return
      }
    }
    throw new Error("no user message to retry")
  }

  private async taskAction(action: string, reason: string): Promise<void> {
    if (!this.task?.task_id) {
      throw new Error("no running task")
    }
    const gw = await this.host.gateway()
    const current = await gw.getTask(this.task.task_id)
    this.task = await gw.controlTask(current.task_id, action, current.version, reason || `vscode ${action}`)
    this.running = this.task.state === "running"
    this.pushSnapshot()
  }

  private async modelSlash(arg: string): Promise<void> {
    const gw = await this.host.gateway()
    const parts = arg.trim().split(/\s+/)
    if (!arg.trim()) {
      const cfg = await gw.modelConfig()
      this.host.setModel(cfg)
      this.post({ type: "model", model: cfg, sessionModel: this.sessionModel })
      return
    }
    if (parts[0] === "main" && parts[1]) {
      await this.setMainModel(parts.slice(1).join(" "))
      return
    }
    await this.preferModel(arg.trim())
  }

  private async showStatus(): Promise<void> {
    const gw = await this.host.gateway()
    const [health, model, mcp] = await Promise.all([gw.health(), gw.modelConfig(), gw.mcpStatus().catch(() => undefined)])
    this.host.setModel(model)
    const lines = [
      `health ${health.ok ? "ok" : "down"}`,
      `model ${model.model || "—"} ready=${model.ready}`,
      this.sessionId ? `session ${this.sessionId}` : "no session",
      mcp ? `mcp ${mcp.ok}/${mcp.total} tools=${mcp.tools}` : "",
    ]
    this.post({ type: "slashResult", kind: "status", body: lines.filter(Boolean).join("\n") })
    if (mcp) {
      this.post({ type: "mcp", mcp })
    }
  }

  private async showSkills(arg: string): Promise<void> {
    const gw = await this.host.gateway()
    const parts = arg.trim().split(/\s+/)
    if (parts[0] === "apply" && parts[1]) {
      await gw.skillAction("apply", parts[1])
    } else if (parts[0] === "reject" && parts[1]) {
      await gw.skillAction("reject", parts[1])
    }
    this.skills = await gw.listSkills(parts[0] === "archived")
    this.post({ type: "skills", skills: this.skills, selected: this.selectedSkills })
  }

  private async showMemory(arg: string): Promise<void> {
    const gw = await this.host.gateway()
    const archived = arg.trim() === "archived"
    const entries = await gw.listMemory(this.sessionId, archived)
    this.post({ type: "memory", entries })
  }

  private async showJourney(arg: string): Promise<void> {
    const gw = await this.host.gateway()
    if (arg.trim() === "skills") {
      const events = await gw.listSkillEvents("", 40)
      this.post({
        type: "journey",
        body: events.map((e) => `${e.created_at} ${e.action} ${e.skill_id}`).join("\n") || "no skill events",
      })
      return
    }
    const entries = await gw.listMemory(this.sessionId, false)
    this.post({
      type: "journey",
      body: entries.map((e) => `${e.kind || e.source}: ${e.content}`).join("\n") || "no memory",
    })
  }

  private async cronSlash(arg: string): Promise<void> {
    const gw = await this.host.gateway()
    const trimmed = arg.trim()
    if (!trimmed) {
      const jobs = await gw.listJobs(false)
      this.post({ type: "jobs", jobs })
      return
    }
    const space = trimmed.search(/\s/)
    if (space < 0) {
      throw new Error("usage: /cron <every> <objective>  (e.g. /cron 15m check status)")
    }
    const every = parseCronEvery(trimmed.slice(0, space))
    const objective = trimmed.slice(space).trim()
    if (!objective) {
      throw new Error("usage: /cron <every> <objective>  (e.g. /cron 15m check status)")
    }
    await this.createJob(every, objective)
  }

  private async createJob(intervalSeconds: number, objective: string): Promise<void> {
    this.requireSession()
    const gw = await this.host.gateway()
    const cfg = this.host.currentModel()
    await gw.createJob({
      name: taskTitle(objective),
      session_id: this.sessionId,
      task_title: taskTitle(objective),
      task_objective: objective,
      execution_mode: executionModeForStance(this.stance),
      skill_ids: this.selectedSkills.length ? this.selectedSkills : undefined,
      model_ref: this.sessionModel || cfg.model || undefined,
      interval_seconds: intervalSeconds,
      next_run_at: new Date(Date.now() + intervalSeconds * 1000).toISOString(),
      idempotency_key: `vscode-${this.sessionId}-${Date.now()}`,
    })
    const jobs = await gw.listJobs(false)
    this.post({ type: "jobs", jobs })
    this.status = `cron every ${intervalSeconds}s`
    this.post({ type: "status", text: this.status })
  }

  private async jobAction(id: string, action: "pause" | "resume" | "cancel"): Promise<void> {
    const gw = await this.host.gateway()
    await gw.jobAction(id, action)
    this.post({ type: "jobs", jobs: await gw.listJobs(false) })
  }

  private async skillAction(action: "apply" | "reject", id: string): Promise<void> {
    const gw = await this.host.gateway()
    await gw.skillAction(action, id)
    this.skills = await gw.listSkills(true)
    this.post({ type: "skills", skills: this.skills, selected: this.selectedSkills })
  }

  private async memoryAction(action: "refresh" | "forget" | "promote", id?: string): Promise<void> {
    const gw = await this.host.gateway()
    if (action === "refresh") {
      await gw.memoryAction({ action: "refresh", session_id: this.sessionId })
    } else if (action === "forget" && id) {
      await gw.memoryAction({ action: "forget", entry_id: id })
    } else if (action === "promote" && id) {
      await gw.memoryAction({ action: "promote", entry_id: id })
    }
    this.post({ type: "memory", entries: await gw.listMemory(this.sessionId, false) })
  }

  private toggleSkill(id: string): void {
    this.selectedSkills = this.selectedSkills.includes(id)
      ? this.selectedSkills.filter((s) => s !== id)
      : [...this.selectedSkills, id]
    this.post({ type: "skills", skills: this.skills, selected: this.selectedSkills })
  }

  private async pickFiles(): Promise<void> {
    const picked = await vscode.window.showOpenDialog({ canSelectMany: true, canSelectFiles: true })
    if (!picked) {
      return
    }
    for (const uri of picked) {
      this.addAbs(uri.fsPath)
    }
  }

  private async searchFiles(query: string): Promise<void> {
    const q = query.trim()
    if (!q) {
      this.post({ type: "files", files: [] })
      return
    }
    const glob = `**/*${q}*`
    const uris = await vscode.workspace.findFiles(glob, "**/node_modules/**", 20)
    const root = workspaceRoot()
    this.post({
      type: "files",
      files: uris.map((u) => {
        const ref = workspaceRelative(u.fsPath, root)
        return { mention: ref.mention, relative: ref.relative }
      }),
    })
  }

  private takeDrops(uris: string[], paths: string[]): void {
    for (const u of uris) {
      try {
        const uri = vscode.Uri.parse(u)
        if (uri.scheme === "file") {
          this.addAbs(uri.fsPath)
        }
      } catch {
        /* skip */
      }
    }
    for (const p of paths) {
      if (p) {
        this.addAbs(p)
      }
    }
  }

  private startStreams(): void {
    if (!this.sessionId) {
      return
    }
    this.stopStreams()
    this.eventsAbort = new AbortController()
    this.modelAbort = new AbortController()
    const eventsSignal = this.eventsAbort.signal
    const modelSignal = this.modelAbort.signal
    void this.host.gateway().then((gw) => {
      const pumpEvents = () => {
        if (eventsSignal.aborted) {
          return
        }
        void gw
          .streamEvents(this.sseAfter, (env) => {
            this.sseAfter = Math.max(this.sseAfter, env.sequence || 0)
            void this.onEnvelope(env.event_type)
          }, eventsSignal)
          .then(() => {
            if (!eventsSignal.aborted) {
              setTimeout(pumpEvents, 800)
            }
          })
          .catch((err) => {
            if (eventsSignal.aborted) {
              return
            }
            this.status = errText(err)
            this.post({ type: "status", text: this.status, error: true })
            setTimeout(pumpEvents, 1500)
          })
      }
      const pumpModel = () => {
        if (modelSignal.aborted || !this.sessionId) {
          return
        }
        void gw
          .streamModel(this.sessionId, (env) => this.onModel(env), modelSignal)
          .then(() => {
            if (!modelSignal.aborted) {
              setTimeout(pumpModel, 800)
            }
          })
          .catch(() => {
            if (!modelSignal.aborted) {
              setTimeout(pumpModel, 1500)
            }
          })
      }
      pumpEvents()
      pumpModel()
    })
  }

  private stopStreams(): void {
    this.eventsAbort?.abort()
    this.modelAbort?.abort()
    this.eventsAbort = undefined
    this.modelAbort = undefined
  }

  private onModel(env: {
    session_id?: string
    parent_run_id?: string
    run_id?: string
    event: { type: string; content_delta?: string; thinking_delta?: string; tool_call?: { id?: string; name?: string; arguments?: string } }
  }): void {
    if (env.session_id && env.session_id !== this.sessionId) {
      return
    }
    if (env.parent_run_id) {
      return
    }
    if (env.run_id) {
      this.live.runId = env.run_id
    }
    const t = env.event.type
    if (t === "delta") {
      this.live.content += env.event.content_delta || ""
      this.post({ type: "live", live: this.live })
    } else if (t === "thinking_delta") {
      this.live.thinking += env.event.thinking_delta || ""
      this.post({ type: "live", live: this.live })
    } else if (t === "tool_call" && env.event.tool_call) {
      this.live.tools = [
        ...this.live.tools,
        {
          id: env.event.tool_call.id || String(this.live.tools.length),
          name: env.event.tool_call.name || "tool",
          preview: (env.event.tool_call.arguments || "").slice(0, 180),
        },
      ]
      this.post({ type: "live", live: this.live })
    } else if (t === "complete") {
      this.resetLive()
      void this.refreshTranscript()
    }
  }

  private async onEnvelope(typ: string): Promise<void> {
    try {
      const gw = await this.host.gateway()
      if (typ.startsWith("permission.")) {
        this.permissions = this.sessionId ? await gw.listPermissions(this.sessionId) : []
        if (this.permissions.length) {
          this.pendingDot = "permission"
        }
        this.panel.title = this.tabTitle()
        this.post({ type: "permissions", permissions: this.permissions })
        return
      }
      if (typ.startsWith("question.")) {
        this.questions = this.sessionId ? await gw.listQuestions(this.sessionId) : []
        this.post({ type: "questions", questions: this.questions })
        return
      }
      if (typ.startsWith("task.") || typ.startsWith("run.")) {
        if (this.task?.task_id) {
          this.task = await gw.getTask(this.task.task_id).catch(() => this.task)
          this.running = this.task?.state === "running"
          if (this.task && this.task.state !== "running" && this.task.state !== "created") {
            if (!this.panel.active) {
              this.pendingDot = this.permissions.length ? "permission" : "done"
            }
            this.resetLive()
          }
        }
        await this.refreshTranscript()
        void this.host.refreshSessions()
      }
    } catch {
      /* ignore poll errors */
    }
  }

  private async refreshTranscript(): Promise<void> {
    if (!this.sessionId) {
      return
    }
    const gw = await this.host.gateway()
    this.messages = await gw.sessionMessages(this.sessionId)
    this.todos = await gw.sessionTodos(this.sessionId).catch(() => this.todos)
    this.contextInfo = await gw.sessionContext(this.sessionId).catch(() => this.contextInfo)
    this.panel.title = this.tabTitle()
    this.post({ type: "transcript", messages: this.messages })
    this.post({ type: "todos", todos: this.todos })
    this.post({ type: "context", context: this.contextInfo })
    this.post({ type: "live", live: this.live })
  }

  private resetLive(): void {
    this.live = { content: "", thinking: "", tools: [], runId: "" }
    this.post({ type: "live", live: this.live })
  }

  private requireSession(): void {
    if (!this.sessionId) {
      throw new Error("focus a session first")
    }
  }
}

function workspaceRoot(): string | undefined {
  return vscode.workspace.workspaceFolders?.[0]?.uri.fsPath
}

export function fileRefFromEditor(editor: vscode.TextEditor): { mention: string; abs: string; outside: boolean } {
  const ref = workspaceRelative(editor.document.uri.fsPath, workspaceRoot())
  const sel = editor.selection
  let mention = ref.mention
  if (!sel.isEmpty) {
    mention = withLineRange(mention, sel.start.line + 1, sel.end.line + 1)
  }
  return { mention, abs: ref.abs, outside: ref.outside }
}

async function openWorkspacePath(p: string): Promise<void> {
  const raw = p.replace(/^@/, "").split("#")[0]
  const root = workspaceRoot()
  const abs = raw.startsWith("/") || /^[a-zA-Z]:[\\/]/.test(raw) ? raw : root ? `${root}/${raw}` : raw
  try {
    const doc = await vscode.workspace.openTextDocument(vscode.Uri.file(abs))
    await vscode.window.showTextDocument(doc, { preview: true, preserveFocus: true })
  } catch {
    void vscode.window.showInformationMessage(`Cannot open ${abs}`)
  }
}

function shortTitle(id: string): string {
  return id.length <= 12 ? id : id.slice(0, 10)
}

function errText(err: unknown): string {
  if (err instanceof GatewayError) {
    return err.message
  }
  return err instanceof Error ? err.message : String(err)
}


