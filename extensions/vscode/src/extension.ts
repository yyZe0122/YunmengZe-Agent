import * as vscode from "vscode"
import { ChatHost } from "./chat/host"

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
    { dispose: () => host?.dispose() },
  )
}

export function deactivate(): void {
  host?.dispose()
  host = undefined
}
