import * as vscode from "vscode"
import type { Session } from "../types"

export class SessionTree implements vscode.TreeDataProvider<SessionItem> {
  private readonly _onDidChange = new vscode.EventEmitter<void>()
  readonly onDidChangeTreeData = this._onDidChange.event
  private sessions: Session[] = []
  private error = ""
  private idle = true

  refresh(sessions: Session[], error = ""): void {
    this.idle = false
    this.sessions = sessions
    this.error = error
    this._onDidChange.fire()
  }

  getTreeItem(element: SessionItem): vscode.TreeItem {
    return element
  }

  getChildren(): SessionItem[] {
    if (this.idle) {
      const item = new SessionItem("", "Open a chat tab to connect", "empty")
      item.contextValue = "ymzIdle"
      return [item]
    }
    if (this.error) {
      const item = new SessionItem("", this.error, "error")
      item.contextValue = "ymzError"
      return [item]
    }
    if (this.sessions.length === 0) {
      const item = new SessionItem("", "No sessions yet", "empty")
      item.contextValue = "ymzEmpty"
      return [item]
    }
    return this.sessions.map((s) => {
      const title = (s.title || s.session_id).trim() || s.session_id
      const item = new SessionItem(s.session_id, title, "session")
      item.description = shortId(s.session_id)
      item.tooltip = `${s.session_id}\n${s.latest_task_state || s.state}`
      item.command = { command: "ymz.openSession", title: "Open", arguments: [s.session_id] }
      const running = s.latest_task_state === "running"
      item.iconPath = new vscode.ThemeIcon(running ? "sync~spin" : "comment-discussion")
      return item
    })
  }
}

export class SessionItem extends vscode.TreeItem {
  constructor(
    readonly sessionId: string,
    label: string,
    kind: "session" | "empty" | "error",
  ) {
    super(label, vscode.TreeItemCollapsibleState.None)
    this.contextValue = kind === "session" ? "ymzSession" : kind
  }
}

function shortId(id: string): string {
  return id.length <= 10 ? id : id.slice(0, 8)
}
