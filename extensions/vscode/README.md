# YunmengZe for VS Code

Two VSIX from this folder (**do not install both** — shortcut clash):

| Asset | Extension | UX |
| --- | --- | --- |
| `ymz-vscode_*.vsix` | YunmengZe | Webview chat: activity-bar session list + drag-able editor tabs. Drag files onto the composer as `@path`. |
| `ymz-vscode-tui_*.vsix` | YunmengZe TUI | Integrated-terminal TUI. Session cwd is the current VS Code folder. |

Talks to the local `ymzd` gateway (GUI) or starts `ymz` in a terminal (TUI). Not on the Marketplace.

Requires [YunmengZe Agent](https://github.com/yyZe0122/YunmengZe-Agent) (`ymz` on `PATH` or `~/.local/bin/ymz`). The extension does not bundle the daemon. Opening VS Code does **not** start `ymzd`; the first GUI chat tab starts it **silently**. Closing VS Code does **not** stop `ymzd`.

## Install (VSIX)

From a GitHub Release or from source: see **[docs/wiki/vscode.md](../../docs/wiki/vscode.md)**.

```bash
make vscode
code --install-extension extensions/vscode/ymz-vscode_*.vsix
# or
code --install-extension extensions/vscode/ymz-vscode-tui_*.vsix
```

## GUI commands

| Command | Shortcut | Action |
| --- | --- | --- |
| YunmengZe: Focus Input | Ctrl/Cmd+Esc | Focus the composer, or open a new tab |
| YunmengZe: Open in New Tab | Ctrl/Cmd+Shift+Esc | Always a new chat tab |
| YunmengZe: Insert @-file reference | Alt/Option+K | `@path` / `#L` into the composer |

Settings: `ymz.executablePath`, `ymz.home`, `ymz.preferredLocation`.

Workspace drops become `@rel/path`. Drops from outside the folder become `@/abs/path` (extra-root `/perm` when the agent reads them). No inline diff; no Marketplace.

## TUI commands

| Command | Shortcut | Action |
| --- | --- | --- |
| YunmengZe TUI: Open | Ctrl/Cmd+Esc | Reuse the `ymz` terminal, or split and start |
| YunmengZe TUI: Open in new tab | Ctrl/Cmd+Shift+Esc | Always a new column |
| YunmengZe TUI: Insert @-file | Alt/Option+K | `@path` / `#L` into the TUI (no Enter) |

No drag-drop chips. Explorer context menu inserts the path as text.

## Develop

Open **this folder** (`extensions/vscode`), not the repo root, then F5.

```bash
cd extensions/vscode
npm install
npm test
# F5 in VS Code
```

Architecture: [ADR-056](../../docs/wiki/adr/056-vscode-webview-chat.md) · [ADR-054](../../docs/wiki/adr/054-vscode-terminal-launcher.md).
