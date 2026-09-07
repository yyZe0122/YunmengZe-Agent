import * as vscode from "vscode"
import { ChatHost, fileRefFromEditor } from "./chat/host"
import { openTuiTerminal } from "./daemon"

let host: ChatHost | undefined

export function activate(context: vscode.ExtensionContext): void {
  host = new ChatHost(context)
  host.activate()
  context.subscriptions.push(
    vscode.commands.registerCommand("ymz.focusInput", () => host?.focusInput()),
    vscode.commands.registerCommand("ymz.openNewTab", () => host?.openNew()),
    vscode.commands.registerCommand("ymz.openSession", (id: string) => host?.openSession(id)),
    vscode.commands.registerCommand("ymz.addFilepath", () => host?.insertAtMention()),
    vscode.commands.registerCommand("ymz.addExplorerPath", (uri: vscode.Uri) => host?.addUri(uri)),
    vscode.commands.registerCommand("ymz.openTerminal", () => openTuiTerminal({ reuse: true })),
    vscode.commands.registerCommand("ymz.openNewTerminal", () => openTuiTerminal({ reuse: false })),
    vscode.commands.registerCommand("ymz.addFilepathToTerminal", () => {
      if (vscode.workspace.getConfiguration("ymz").get<boolean>("useTerminal")) {
        addFilepathToTerminal()
        return
      }
      void host?.insertAtMention()
    }),
    { dispose: () => host?.dispose() },
  )
}

export function deactivate(): void {
  host?.dispose()
  host = undefined
}

function addFilepathToTerminal(): void {
  const editor = vscode.window.activeTextEditor
  if (!editor) {
    return
  }
  const ref = fileRefFromEditor(editor)
  const terminal =
    vscode.window.activeTerminal?.name === "ymz"
      ? vscode.window.activeTerminal
      : vscode.window.terminals.find((t) => t.name === "ymz")
  if (!terminal) {
    void vscode.window.showInformationMessage("Open ymz first (YunmengZe: Open TUI).")
    return
  }
  terminal.show()
  terminal.sendText(ref.mention, false)
}
