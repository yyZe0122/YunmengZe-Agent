import * as fs from "fs"
import * as os from "os"
import * as path from "path"
import { spawn } from "child_process"
import * as vscode from "vscode"
import { Gateway } from "./gateway"
import { workspaceRelative } from "./paths"
import { fileRefFromEditor, workspaceRoot } from "./workspace"

const TERMINAL_NAME = "ymz"

export function resolveYmz(): string | undefined {
  const configured = vscode.workspace.getConfiguration("ymz").get<string>("executablePath")?.trim()
  if (configured && isUsableFile(configured)) {
    return configured
  }
  const exe = process.platform === "win32" ? "ymz.exe" : "ymz"
  const fromPath = findOnPath(exe)
  if (fromPath) {
    return fromPath
  }
  const fallback = path.join(os.homedir(), ".local", "bin", exe)
  if (isUsableFile(fallback)) {
    return fallback
  }
  return undefined
}

export async function ensureGateway(): Promise<Gateway> {
  const gw = Gateway.fromConfig()
  if (await ping(gw)) {
    return gw
  }
  const bin = resolveYmz()
  if (!bin) {
    throw new Error("ymz was not found on PATH, ~/.local/bin, or ymz.executablePath. Install YunmengZe, then reload.")
  }
  await startDaemon(bin)
  const deadline = Date.now() + 12_000
  while (Date.now() < deadline) {
    if (await ping(gw)) {
      return gw
    }
    await sleep(250)
  }
  throw new Error("ymzd did not become healthy. Check ymz status / logs.")
}

export function openTuiTerminal(opts?: { reuse?: boolean }): void {
  if (opts?.reuse !== false) {
    const existing = vscode.window.terminals.find((t) => t.name === TERMINAL_NAME)
    if (existing) {
      existing.show()
      return
    }
  }
  const bin = resolveYmz()
  if (!bin) {
    void vscode.window.showErrorMessage(
      "ymz was not found on PATH or in ~/.local/bin. Install YunmengZe, then reload the window.",
    )
    return
  }
  const cwd = workspaceRoot()
  const terminal = vscode.window.createTerminal({
    name: TERMINAL_NAME,
    ...(cwd ? { cwd } : {}),
    iconPath: {
      light: vscode.Uri.file(path.join(__dirname, "..", "images", "button-dark.svg")),
      dark: vscode.Uri.file(path.join(__dirname, "..", "images", "button-light.svg")),
    },
    location: { viewColumn: vscode.ViewColumn.Beside, preserveFocus: false },
  })
  terminal.show()
  terminal.sendText(shellQuote(bin))
}

export function insertMentionIntoTerminal(): void {
  const editor = vscode.window.activeTextEditor
  if (!editor || editor.document.uri.scheme !== "file") {
    return
  }
  sendToYmzTerminal(fileRefFromEditor(editor).mention)
}

export function addUriToTerminal(uri: vscode.Uri): void {
  if (uri.scheme !== "file") {
    return
  }
  sendToYmzTerminal(workspaceRelative(uri.fsPath, workspaceRoot()).mention)
}

function sendToYmzTerminal(text: string): void {
  const terminal =
    vscode.window.activeTerminal?.name === TERMINAL_NAME
      ? vscode.window.activeTerminal
      : vscode.window.terminals.find((t) => t.name === TERMINAL_NAME)
  if (!terminal) {
    void vscode.window.showInformationMessage("Open YunmengZe TUI first (YunmengZe TUI: Open).")
    return
  }
  terminal.show()
  terminal.sendText(text, false)
}

function findOnPath(exe: string): string | undefined {
  const pathEnv = process.env.PATH ?? ""
  const sep = process.platform === "win32" ? ";" : ":"
  for (const dir of pathEnv.split(sep)) {
    if (!dir) {
      continue
    }
    const candidate = path.join(dir, exe)
    if (isUsableFile(candidate)) {
      return candidate
    }
  }
  return undefined
}

function isUsableFile(candidate: string): boolean {
  try {
    const mode = process.platform === "win32" ? fs.constants.F_OK : fs.constants.X_OK
    fs.accessSync(candidate, mode)
    return fs.statSync(candidate).isFile()
  } catch {
    return false
  }
}

function shellQuote(bin: string): string {
  if (process.platform === "win32") {
    if (/[\s"]/.test(bin)) {
      return `"${bin.replace(/"/g, '\\"')}"`
    }
    return bin
  }
  if (/[^a-zA-Z0-9_./+-]/.test(bin)) {
    return `'${bin.replace(/'/g, `'\\''`)}'`
  }
  return bin
}

async function ping(gw: Gateway): Promise<boolean> {
  try {
    const h = await gw.health()
    return !!h.ok
  } catch {
    return false
  }
}

function startDaemon(bin: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const cwd = workspaceRoot()
    const child = spawn(bin, ["start"], {
      ...(cwd ? { cwd } : {}),
      detached: true,
      stdio: "ignore",
      windowsHide: true,
      env: process.env,
    })
    let settled = false
    const done = (err?: Error) => {
      if (settled) {
        return
      }
      settled = true
      if (err) {
        reject(err)
        return
      }
      resolve()
    }
    child.on("error", (err) => done(err))
    child.unref()
    child.on("exit", (code) => {
      if (code && code !== 0) {
        done(new Error(`ymz start exited ${code}`))
        return
      }
      done()
    })
    setTimeout(() => done(), 800)
  })
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms))
}
