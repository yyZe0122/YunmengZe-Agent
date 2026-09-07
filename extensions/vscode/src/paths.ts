import * as path from "path"

export type FileRef = {
  mention: string
  abs: string
  relative: string
  outside: boolean
}

function toPosix(p: string): string {
  return p.replace(/\\/g, "/")
}

export function workspaceRelative(absPath: string, workspaceRoot: string | undefined): FileRef {
  const abs = path.resolve(absPath)
  const root = workspaceRoot ? path.resolve(workspaceRoot) : ""
  if (!root) {
    return { mention: `@${toPosix(abs)}`, abs, relative: toPosix(abs), outside: true }
  }
  const rel = path.relative(root, abs)
  const outside = rel === "" ? false : rel.startsWith("..") || path.isAbsolute(rel)
  if (outside) {
    return { mention: `@${toPosix(abs)}`, abs, relative: toPosix(abs), outside: true }
  }
  const relative = toPosix(rel) || path.basename(abs)
  return { mention: `@${relative}`, abs, relative, outside: false }
}

export function withLineRange(mention: string, startLine: number, endLine: number): string {
  if (!Number.isFinite(startLine) || startLine < 1) {
    return mention
  }
  if (!Number.isFinite(endLine) || endLine <= startLine) {
    return `${mention}#L${startLine}`
  }
  return `${mention}#L${startLine}-${endLine}`
}

export function composeMessage(text: string, mentions: string[]): string {
  const chips = mentions.map((m) => m.trim()).filter(Boolean)
  const body = text.replace(/\s+$/, "")
  if (chips.length === 0) {
    return body
  }
  const block = chips.join(" ")
  if (!body) {
    return block
  }
  return `${body}\n\n${block}`
}

const BUILTIN_SLASH = new Set([
  "quit",
  "help",
  "status",
  "model",
  "skills",
  "theme",
  "cron",
  "compact",
  "undo",
  "edit",
  "editundo",
  "perm",
  "expand",
  "journey",
  "memory",
  "refresh-memory",
  "new",
  "pause",
  "resume",
  "cancel",
  "retry",
  "back",
  "sessions",
  "tasks",
])

const DURATION_UNIT: Record<string, number> = {
  ns: 1e-9,
  us: 1e-6,
  µs: 1e-6,
  μs: 1e-6,
  ms: 1e-3,
  s: 1,
  m: 60,
  h: 3600,
}

export function parseCronEvery(raw: string): number {
  const trimmed = raw.trim()
  if (!trimmed) {
    throw new Error("interval is required (Go duration, e.g. 15m, 1h)")
  }
  const re = /([0-9]+(?:\.[0-9]*)?|\.[0-9]+)(ns|us|µs|μs|ms|s|m|h)/g
  let seconds = 0
  let consumed = 0
  let match: RegExpExecArray | null
  while ((match = re.exec(trimmed))) {
    if (match.index !== consumed) {
      break
    }
    seconds += Number(match[1]) * DURATION_UNIT[match[2]]
    consumed = re.lastIndex
  }
  if (consumed === 0 || consumed !== trimmed.length || !Number.isFinite(seconds)) {
    throw new Error(`invalid interval ${JSON.stringify(trimmed)} (use Go duration, e.g. 15m, 1h)`)
  }
  if (seconds < 1) {
    throw new Error("interval must be at least 1s")
  }
  return Math.round(seconds)
}

export function parseSlash(line: string): { name: string; arg: string } | undefined {
  const trimmed = line.trim()
  if (!trimmed.startsWith("/")) {
    return undefined
  }
  const space = trimmed.search(/\s/)
  const raw = space < 0 ? trimmed : trimmed.slice(0, space)
  const arg = space < 0 ? "" : trimmed.slice(space).trim()
  const id = raw.slice(1).trim()
  if (!id) {
    return undefined
  }
  return { name: id, arg }
}

export function isBuiltinSlash(name: string): boolean {
  return BUILTIN_SLASH.has(name.replace(/^\//, "").toLowerCase())
}

export function expandChatCommandTemplate(template: string, args: string): string {
  const t = template.replace(/[ \t]+$/g, "")
  const a = args.trim()
  if (t.includes("$ARGUMENTS")) {
    return t.split("$ARGUMENTS").join(a)
  }
  if (t.includes("$0")) {
    return t.split("$0").join(a)
  }
  if (!a) {
    return t.trim()
  }
  if (!t.trim()) {
    return a
  }
  return `${t.trim()}\n\n${a}`
}

export function taskTitle(objective: string): string {
  const runes = Array.from(objective.trim())
  if (runes.length <= 80) {
    return runes.join("")
  }
  return `${runes.slice(0, 80).join("")}…`
}

export function normalizeDecision(raw: string): string | undefined {
  switch (raw.trim().toLowerCase()) {
    case "once":
    case "allow_once":
      return "allow_once"
    case "similar":
    case "allow_similar":
      return "allow_similar"
    case "permanent":
    case "always":
    case "allow_permanent":
      return "allow_permanent"
    case "deny":
      return "deny"
    default:
      return undefined
  }
}

export function executionModeForStance(stance: string): "agent" | "plan" {
  return stance === "plan" ? "plan" : "agent"
}

export function imageLike(p: string): boolean {
  return /\.(png|jpe?g|gif|webp|bmp|svg|avif)$/i.test(p)
}

export function videoLike(p: string): boolean {
  return /\.(mp4|webm|mkv|mov|avi)$/i.test(p)
}
