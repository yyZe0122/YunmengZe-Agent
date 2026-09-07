import * as fs from "fs"
import * as http from "http"
import * as os from "os"
import * as path from "path"
import * as vscode from "vscode"
import type {
  ChatCommand,
  Envelope,
  Job,
  MCPStatus,
  MemoryEntry,
  ModelConfig,
  ModelStreamEvent,
  Permission,
  Session,
  SessionTodo,
  Skill,
  SkillEvent,
  SubmitResult,
  Task,
  TaskContext,
  TranscriptMessage,
  UserQuestion,
} from "./types"

type Endpoint = {
  network: string
  address: string
  token?: string
}

export class GatewayError extends Error {
  constructor(
    readonly status: number,
    readonly body: string,
  ) {
    super(body ? `gateway ${status}: ${body}` : `gateway ${status}`)
  }
}

export class Gateway {
  constructor(private readonly runtimeDir: string) {}

  static fromConfig(): Gateway {
    const override = vscode.workspace.getConfiguration("ymz").get<string>("home")?.trim()
    const envHome = process.env.YMZ_HOME?.trim()
    const root = override || envHome || path.join(os.homedir(), ".yunmengze")
    return new Gateway(path.join(root, "run"))
  }

  async health(): Promise<{ ok: boolean }> {
    return this.json("GET", "/v1/health")
  }

  async modelConfig(): Promise<ModelConfig> {
    return this.json("GET", "/v1/config/model")
  }

  async setModelConfig(model: string): Promise<ModelConfig> {
    return this.json("PUT", "/v1/config/model", { model })
  }

  async mcpStatus(): Promise<MCPStatus> {
    return this.json("GET", "/v1/config/mcp")
  }

  async listCommands(): Promise<ChatCommand[]> {
    const r = await this.json<{ commands?: ChatCommand[] }>("GET", "/v1/config/commands")
    return r.commands ?? []
  }

  async listSessions(limit = 80): Promise<Session[]> {
    const r = await this.json<{ sessions?: Session[] }>("GET", `/v1/sessions?limit=${limit}`)
    return r.sessions ?? []
  }

  async getSession(id: string): Promise<Session> {
    return this.json("GET", `/v1/sessions/${encodeURIComponent(id)}`)
  }

  async patchSession(id: string, body: { preferred_model?: string; permission_stance?: string }): Promise<Session> {
    return this.json("PATCH", `/v1/sessions/${encodeURIComponent(id)}`, body)
  }

  async sessionMessages(id: string, limit = 400): Promise<TranscriptMessage[]> {
    const r = await this.json<{ messages?: TranscriptMessage[] }>(
      "GET",
      `/v1/sessions/${encodeURIComponent(id)}/messages?limit=${limit}`,
    )
    return r.messages ?? []
  }

  async sessionTodos(id: string): Promise<SessionTodo[]> {
    const r = await this.json<{ todos?: SessionTodo[] }>("GET", `/v1/sessions/${encodeURIComponent(id)}/todos`)
    return r.todos ?? []
  }

  async sessionContext(id: string): Promise<TaskContext> {
    return this.json("GET", `/v1/sessions/${encodeURIComponent(id)}/context`)
  }

  async compact(id: string, focus = ""): Promise<{ summary: string }> {
    return this.json("POST", `/v1/sessions/${encodeURIComponent(id)}/compact`, focus ? { focus } : {})
  }

  async rewind(id: string): Promise<{ path: string }> {
    return this.json("POST", `/v1/sessions/${encodeURIComponent(id)}/rewind`, {})
  }

  async retract(id: string, rewindFiles: boolean): Promise<{ user_text: string; task_id: string }> {
    return this.json("POST", `/v1/sessions/${encodeURIComponent(id)}/retract`, { rewind_files: rewindFiles })
  }

  async steer(id: string, text: string): Promise<{ task_id: string; run_id: string; item_id: string }> {
    return this.json("POST", `/v1/sessions/${encodeURIComponent(id)}/steer`, { text })
  }

  async submit(body: {
    session_id?: string
    title: string
    objective: string
    execution_mode: string
    workspace: string
    permission_stance: string
    preferred_model?: string
    skill_ids?: string[]
    interactive: boolean
  }): Promise<SubmitResult> {
    return this.json("POST", "/v1/tasks", body)
  }

  async getTask(id: string): Promise<Task> {
    return this.json("GET", `/v1/tasks/${encodeURIComponent(id)}`)
  }

  async controlTask(id: string, action: string, expectedVersion: number, reason: string): Promise<Task> {
    return this.json("POST", `/v1/tasks/${encodeURIComponent(id)}/actions`, {
      expected_version: expectedVersion,
      action,
      reason,
    })
  }

  async listPermissions(sessionId: string): Promise<Permission[]> {
    const q = sessionId ? `?session_id=${encodeURIComponent(sessionId)}&limit=50` : "?limit=50"
    const r = await this.json<{ permissions?: Permission[] }>("GET", `/v1/permissions${q}`)
    return r.permissions ?? []
  }

  async decidePermission(id: string, decision: string, confirm = false): Promise<Permission> {
    return this.json("POST", `/v1/permissions/${encodeURIComponent(id)}/decide`, {
      decision,
      actor: "vscode",
      confirm,
    })
  }

  async listQuestions(sessionId: string): Promise<UserQuestion[]> {
    const q = sessionId ? `?session_id=${encodeURIComponent(sessionId)}&limit=20` : "?limit=20"
    const r = await this.json<{ questions?: UserQuestion[] }>("GET", `/v1/questions${q}`)
    return r.questions ?? []
  }

  async answerQuestion(id: string, answers: Record<string, string[]>): Promise<UserQuestion> {
    return this.json("POST", `/v1/questions/${encodeURIComponent(id)}/answer`, { answers, actor: "vscode" })
  }

  async dismissQuestion(id: string): Promise<UserQuestion> {
    return this.json("POST", `/v1/questions/${encodeURIComponent(id)}/dismiss`, { actor: "vscode" })
  }

  async listSkills(includeArchived = false): Promise<Skill[]> {
    const q = includeArchived ? "?include_archived=true" : ""
    const r = await this.json<{ skills?: Skill[] }>("GET", `/v1/skills${q}`)
    return r.skills ?? []
  }

  async listSkillEvents(skillId = "", limit = 40): Promise<SkillEvent[]> {
    const q = new URLSearchParams()
    if (skillId) {
      q.set("skill_id", skillId)
    }
    q.set("limit", String(limit))
    const r = await this.json<{ events?: SkillEvent[] }>("GET", `/v1/skills/events?${q.toString()}`)
    return r.events ?? []
  }

  async skillAction(action: "apply" | "reject", skillId: string): Promise<void> {
    await this.json("POST", "/v1/skills/actions", { action, skill_id: skillId, actor: "vscode" })
  }

  async listMemory(sessionId: string, includeArchived = false): Promise<MemoryEntry[]> {
    const q = new URLSearchParams()
    if (sessionId) {
      q.set("session_id", sessionId)
    }
    q.set("include_global", "true")
    if (includeArchived) {
      q.set("include_archived", "true")
    }
    q.set("limit", "80")
    const r = await this.json<{ entries?: MemoryEntry[] }>("GET", `/v1/memory?${q.toString()}`)
    return r.entries ?? []
  }

  async memoryAction(body: Record<string, string>): Promise<void> {
    await this.json("POST", "/v1/memory/actions", body)
  }

  async listJobs(includeArchived = false): Promise<Job[]> {
    const q = includeArchived ? "?include_archived=true" : ""
    const r = await this.json<{ jobs?: Job[] }>("GET", `/v1/jobs${q}`)
    return r.jobs ?? []
  }

  async createJob(body: Record<string, unknown>): Promise<Job> {
    return this.json("POST", "/v1/jobs", body)
  }

  async jobAction(id: string, action: string): Promise<Job> {
    return this.json("POST", `/v1/jobs/${encodeURIComponent(id)}/actions`, {
      action,
      reviewer: "vscode",
      reason: action,
    })
  }

  streamEvents(after: number, onEvent: (env: Envelope) => void, signal: AbortSignal): Promise<void> {
    const q = after > 0 ? `?after=${after}` : ""
    return this.sse(`/v1/events/stream${q}`, after, (ev) => {
      if (!ev.data) {
        return
      }
      onEvent(JSON.parse(ev.data) as Envelope)
    }, signal)
  }

  streamModel(sessionId: string, onEvent: (env: ModelStreamEvent) => void, signal: AbortSignal): Promise<void> {
    const q = sessionId ? `?session_id=${encodeURIComponent(sessionId)}` : ""
    return this.sse(`/v1/model-stream${q}`, 0, (ev) => {
      if (!ev.data) {
        return
      }
      onEvent(JSON.parse(ev.data) as ModelStreamEvent)
    }, signal)
  }

  private readEndpoint(): Endpoint {
    const file = path.join(this.runtimeDir, "gateway.json")
    const raw = fs.readFileSync(file, "utf8")
    const ep = JSON.parse(raw) as Endpoint
    validateEndpoint(this.runtimeDir, ep)
    return ep
  }

  private async json<T>(method: string, urlPath: string, body?: unknown): Promise<T> {
    const payload = body === undefined ? undefined : Buffer.from(JSON.stringify(body))
    const { status, data } = await this.request(method, urlPath, payload, "application/json")
    if (status < 200 || status >= 300) {
      throw new GatewayError(status, data.toString("utf8").trim())
    }
    if (!data.length) {
      return {} as T
    }
    return JSON.parse(data.toString("utf8")) as T
  }

  private request(
    method: string,
    urlPath: string,
    body: Buffer | undefined,
    accept: string,
  ): Promise<{ status: number; data: Buffer; headers: http.IncomingHttpHeaders }> {
    const ep = this.readEndpoint()
    const headers: http.OutgoingHttpHeaders = { Accept: accept }
    if (body) {
      headers["Content-Type"] = "application/json"
      headers["Content-Length"] = body.length
    }
    if (ep.token) {
      headers.Authorization = `Bearer ${ep.token}`
    }
    const opts: http.RequestOptions = { method, headers, path: urlPath }
    if (ep.network === "unix") {
      opts.socketPath = ep.address
      opts.host = "local"
    } else if (ep.network === "tcp") {
      const [hostname, port] = splitHostPort(ep.address)
      opts.hostname = hostname
      opts.port = port
    } else {
      return Promise.reject(new Error(`unsupported gateway network ${ep.network}`))
    }
    return new Promise((resolve, reject) => {
      const req = http.request(opts, (res) => {
        const chunks: Buffer[] = []
        res.on("data", (c) => chunks.push(c as Buffer))
        res.on("end", () => {
          resolve({ status: res.statusCode ?? 0, data: Buffer.concat(chunks), headers: res.headers })
        })
      })
      req.on("error", reject)
      if (body) {
        req.write(body)
      }
      req.end()
    })
  }

  private sse(
    urlPath: string,
    after: number,
    emit: (ev: { id: string; event: string; data: string }) => void,
    signal: AbortSignal,
  ): Promise<void> {
    const ep = this.readEndpoint()
    const headers: http.OutgoingHttpHeaders = { Accept: "text/event-stream" }
    if (after > 0) {
      headers["Last-Event-ID"] = String(after)
    }
    if (ep.token) {
      headers.Authorization = `Bearer ${ep.token}`
    }
    const opts: http.RequestOptions = { method: "GET", headers, path: urlPath }
    if (ep.network === "unix") {
      opts.socketPath = ep.address
      opts.host = "local"
    } else {
      const [hostname, port] = splitHostPort(ep.address)
      opts.hostname = hostname
      opts.port = port
    }
    return new Promise((resolve, reject) => {
      const req = http.request(opts, (res) => {
        if ((res.statusCode ?? 0) < 200 || (res.statusCode ?? 0) >= 300) {
          const chunks: Buffer[] = []
          res.on("data", (c) => chunks.push(c as Buffer))
          res.on("end", () => {
            reject(new GatewayError(res.statusCode ?? 0, Buffer.concat(chunks).toString("utf8").trim()))
          })
          return
        }
        let buf = ""
        let current: { id: string; event: string; data: string } = { id: "", event: "", data: "" }
        const flush = () => {
          if (current.data || current.event || current.id) {
            emit(current)
          }
          current = { id: "", event: "", data: "" }
        }
        res.on("data", (chunk) => {
          buf += chunk.toString("utf8")
          let idx: number
          while ((idx = buf.indexOf("\n")) >= 0) {
            let line = buf.slice(0, idx)
            buf = buf.slice(idx + 1)
            if (line.endsWith("\r")) {
              line = line.slice(0, -1)
            }
            if (!line) {
              flush()
              continue
            }
            if (line.startsWith(":")) {
              continue
            }
            const cut = line.indexOf(":")
            const field = cut < 0 ? line : line.slice(0, cut)
            let value = cut < 0 ? "" : line.slice(cut + 1)
            if (value.startsWith(" ")) {
              value = value.slice(1)
            }
            if (field === "id") {
              current.id = value
            } else if (field === "event") {
              current.event = value
            } else if (field === "data") {
              current.data = current.data ? `${current.data}\n${value}` : value
            }
          }
        })
        res.on("end", () => {
          if (signal.aborted) {
            resolve()
            return
          }
          resolve()
        })
        res.on("error", (err) => {
          if (signal.aborted) {
            resolve()
            return
          }
          reject(err)
        })
      })
      req.on("error", (err) => {
        if (signal.aborted) {
          resolve()
          return
        }
        reject(err)
      })
      const abort = () => {
        req.destroy()
      }
      if (signal.aborted) {
        abort()
        resolve()
        return
      }
      signal.addEventListener("abort", abort, { once: true })
      req.end()
    })
  }
}

function validateEndpoint(runtimeDir: string, ep: Endpoint): void {
  if (!ep.network || !ep.address) {
    throw new Error("gateway endpoint is incomplete")
  }
  if (ep.network === "unix") {
    const address = path.resolve(ep.address)
    const root = path.resolve(runtimeDir)
    const rel = path.relative(root, address)
    if (!rel || rel === ".." || rel.startsWith(`..${path.sep}`) || path.isAbsolute(rel)) {
      throw new Error("gateway Unix socket escapes runtime directory")
    }
    return
  }
  if (ep.network === "tcp") {
    const [hostname] = splitHostPort(ep.address)
    if (!isLoopbackHost(hostname)) {
      throw new Error("gateway TCP address must be loopback")
    }
    if (!ep.token) {
      throw new Error("gateway TCP endpoint requires authentication")
    }
    return
  }
  throw new Error(`unsupported gateway network ${ep.network}`)
}

function isLoopbackHost(host: string): boolean {
  const h = host.replace(/^\[|\]$/g, "").toLowerCase()
  return h === "127.0.0.1" || h === "::1" || h === "localhost"
}

function splitHostPort(address: string): [string, number] {
  if (address.startsWith("[")) {
    const end = address.indexOf("]")
    if (end < 0 || address[end + 1] !== ":") {
      throw new Error(`invalid gateway address ${address}`)
    }
    return [address.slice(1, end), Number(address.slice(end + 2))]
  }
  const idx = address.lastIndexOf(":")
  if (idx < 0) {
    throw new Error(`invalid gateway address ${address}`)
  }
  return [address.slice(0, idx), Number(address.slice(idx + 1))]
}
