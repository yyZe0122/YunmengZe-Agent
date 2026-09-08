import * as vscode from "vscode"
import { withLineRange, workspaceRelative, type FileRef } from "./paths"

export function workspaceRoot(): string | undefined {
  return vscode.workspace.workspaceFolders?.[0]?.uri.fsPath
}

export function fileRefFromEditor(editor: vscode.TextEditor): FileRef {
  const ref = workspaceRelative(editor.document.uri.fsPath, workspaceRoot())
  const sel = editor.selection
  if (sel.isEmpty) {
    return ref
  }
  return {
    ...ref,
    mention: withLineRange(ref.mention, sel.start.line + 1, sel.end.line + 1),
  }
}
