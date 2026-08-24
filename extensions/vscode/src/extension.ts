import * as fs from "fs"
import * as os from "os"
import * as path from "path"
import * as vscode from "vscode"

const TERMINAL_NAME = "ymz"

export function activate(context: vscode.ExtensionContext): void {
  context.subscriptions.push(
    vscode.commands.registerCommand("ymz.openTerminal", () => openOrFocusTerminal()),
    vscode.commands.registerCommand("ymz.openNewTerminal", () => openNewTerminal()),
    vscode.commands.registerCommand("ymz.addFilepathToTerminal", () => addFilepathToTerminal()),
  )
}

export function deactivate(): void {}

async function openOrFocusTerminal(): Promise<void> {
  const existing = vscode.window.terminals.find((t) => t.name === TERMINAL_NAME)
  if (existing) {
    existing.show()
    return
  }
  await openNewTerminal()
}

async function openNewTerminal(): Promise<void> {
  const bin = resolveYmz()
  if (!bin) {
    void vscode.window.showErrorMessage(
      "ymz was not found on PATH or in ~/.local/bin. Install YunmengZe, then reload the window.",
    )
    return
  }
  const terminal = vscode.window.createTerminal({
    name: TERMINAL_NAME,
    iconPath: {
      light: vscode.Uri.file(path.join(__dirname, "..", "images", "button-dark.svg")),
      dark: vscode.Uri.file(path.join(__dirname, "..", "images", "button-light.svg")),
    },
    location: {
      viewColumn: vscode.ViewColumn.Beside,
      preserveFocus: false,
    },
  })
  terminal.show()
  terminal.sendText(shellQuote(bin))
}

function addFilepathToTerminal(): void {
  const fileRef = getActiveFile()
  if (!fileRef) {
    return
  }
  const terminal =
    vscode.window.activeTerminal?.name === TERMINAL_NAME
      ? vscode.window.activeTerminal
      : vscode.window.terminals.find((t) => t.name === TERMINAL_NAME)
  if (!terminal) {
    void vscode.window.showInformationMessage("Open ymz first (Ctrl/Cmd+Esc).")
    return
  }
  terminal.show()
  terminal.sendText(fileRef, false)
}

function resolveYmz(): string | undefined {
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

function getActiveFile(): string | undefined {
  const activeEditor = vscode.window.activeTextEditor
  if (!activeEditor) {
    return undefined
  }
  const document = activeEditor.document
  if (document.uri.scheme !== "file") {
    return undefined
  }
  const workspaceFolder = vscode.workspace.getWorkspaceFolder(document.uri)
  const rel = workspaceFolder
    ? vscode.workspace.asRelativePath(document.uri, false)
    : document.uri.fsPath
  let ref = `@${rel}`
  const selection = activeEditor.selection
  if (!selection.isEmpty) {
    const startLine = selection.start.line + 1
    const endLine = selection.end.line + 1
    if (startLine === endLine) {
      ref += `#L${startLine}`
    } else {
      ref += `#L${startLine}-${endLine}`
    }
  }
  return ref
}
