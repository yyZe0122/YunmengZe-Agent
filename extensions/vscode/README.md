# YunmengZe for VS Code

Native chat in the editor: activity-bar session list + drag-able editor tabs. Drag files onto the composer to insert `@path` chips. Talks to the local `ymzd` gateway. Not on the Marketplace.

Requires [YunmengZe Agent](https://github.com/yyZe0122/YunmengZe-Agent) (`ymz` on `PATH` or `~/.local/bin/ymz`). The extension does not bundle the daemon. Opening VS Code does **not** start `ymzd`; the first chat tab does. Closing VS Code does **not** stop `ymzd`.

## Install (VSIX)

From a GitHub Release or from source: see **[docs/wiki/vscode.md](../../docs/wiki/vscode.md)**.

```bash
make vscode
code --install-extension extensions/vscode/ymz-vscode_*.vsix
```

## Commands

| Command | Shortcut | Action |
| --- | --- | --- |
| YunmengZe: Focus Input | Ctrl/Cmd+Esc | Focus the composer, or open a new tab |
| YunmengZe: Open in New Tab | Ctrl/Cmd+Shift+Esc | Always a new chat tab |
| YunmengZe: Insert @-file reference | Alt/Option+K | `@path` / `#L` into the composer |
| YunmengZe: Open TUI | — | Integrated-terminal TUI (`ymz.useTerminal` to bind Esc here) |

Settings: `ymz.executablePath`, `ymz.home`, `ymz.useTerminal`, `ymz.preferredLocation`.

Workspace drops become `@rel/path`. Drops from outside the folder become `@/abs/path` (extra-root `/perm` when the agent reads them). No inline diff; no Marketplace.

## Develop

Open **this folder** (`extensions/vscode`), not the repo root, then F5.

```bash
cd extensions/vscode
npm install
npm test
# F5 in VS Code
```

Architecture: [ADR-056](../../docs/wiki/adr/056-vscode-webview-chat.md) · [ADR-054](../../docs/wiki/adr/054-vscode-terminal-launcher.md).
