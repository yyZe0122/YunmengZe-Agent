import * as vscode from "vscode"
import { addUriToTerminal, insertMentionIntoTerminal, openTuiTerminal } from "./daemon"

export function activate(context: vscode.ExtensionContext): void {
  context.subscriptions.push(
    vscode.commands.registerCommand("ymz.tui.open", () => openTuiTerminal({ reuse: true })),
    vscode.commands.registerCommand("ymz.tui.openNew", () => openTuiTerminal({ reuse: false })),
    vscode.commands.registerCommand("ymz.tui.addFilepath", () => insertMentionIntoTerminal()),
    vscode.commands.registerCommand("ymz.tui.addExplorerPath", (uri: vscode.Uri) => addUriToTerminal(uri)),
  )
}

export function deactivate(): void {}
