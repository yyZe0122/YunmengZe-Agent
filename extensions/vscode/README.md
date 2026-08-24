# YunmengZe for VS Code

Opens the existing **ymz TUI** in a split integrated terminal. Not a chat panel. Not on the Marketplace.

Requires [YunmengZe Agent](https://github.com/yyZe0122/YunmengZe-Agent) (`ymz` on `PATH` or `~/.local/bin/ymz`). The extension does not bundle the daemon.

## Install (VSIX)

From a GitHub Release or from source: see **[docs/wiki/vscode.md](../../docs/wiki/vscode.md)** (this README is what the Marketplace/VSIX listing shows; keep it short).

```bash
make vscode
code --install-extension extensions/vscode/ymz-vscode_*.vsix
```

## Commands

| Command | Shortcut | Action |
| --- | --- | --- |
| YunmengZe: Open TUI | Ctrl/Cmd+Esc | Focus an existing `ymz` terminal, or open one beside the editor |
| YunmengZe: Open TUI in new tab | Ctrl/Cmd+Shift+Esc | Always open a new split terminal |
| YunmengZe: Insert @-file reference | Ctrl+Alt+K / Cmd+Alt+K | Insert `@path`, `@path#L12`, or `@path#L12-20` (no Enter) |

The editor title bar has the same “new tab” button.

Closing VS Code does **not** stop `ymzd`. Use `ymz stop`.

If `ymz` is missing, the extension shows an error instead of sending a command that would fail.

## Develop

Open **this folder** (`extensions/vscode`), not the repo root, then F5.

```bash
cd extensions/vscode
npm install
# F5 in VS Code
```

Architecture: [ADR-054](../../docs/wiki/adr/054-vscode-terminal-launcher.md).
