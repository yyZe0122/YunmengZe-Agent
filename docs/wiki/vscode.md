# VS Code / Cursor 扩展

用户安装与发版挂包。架构：[ADR-056](adr/056-vscode-webview-chat.md)（Webview 聊天）· [ADR-054](adr/054-vscode-terminal-launcher.md)（TUI 启动器回退）。Marketplace 仍只在 [`docs/backlog/current.md`](../backlog/current.md)。

源码：[`extensions/vscode/`](../../extensions/vscode/)。扩展 **不** 进 `go.mod`；`make check` 不跑 npm。

## 它做什么

Claude Code 式本机 GUI：活动栏会话列表 + 可拖到侧栏的编辑器 Tab。走本地 Gateway `/v1/*`（`interactive: true`）。扩展 **不** 跑 tool / provider，**不** 把 `ymzd` 打进 VSIX。

需要本机已有 `ymz`（`PATH`、`~/.local/bin/ymz`，或设置 `ymz.executablePath`）。找不到会报错。关编辑器 **不停** daemon（`ymz stop`）。**activate 不拉 daemon**；第一次开聊天 Tab / Focus Input / 拖文件才 `ymz start`。

| 命令 | 快捷键 | 行为 |
| --- | --- | --- |
| YunmengZe: Focus Input | Ctrl/Cmd+Esc | 已有聊天 Tab 则 focus 输入框；否则开新 Tab |
| YunmengZe: Open in New Tab | Ctrl/Cmd+Shift+Esc | 始终新会话 Tab（编辑器标题栏同一按钮） |
| YunmengZe: Insert @-file reference | Alt+K / Option+K | 当前文件/选区写入输入框：`@path` / `@path#L12` / `@path#L12-20` |
| YunmengZe: Open TUI | （无默认键） | 集成终端启动 TUI；设置 `ymz.useTerminal` 时 Esc reuse / Shift+Esc 新开 |

拖工作区文件进输入框 → `@rel/path`。拖工作区外图片/视频/文件 → `@/abs/path`（不拷贝）；agent 读盘时走 extra-root `/perm`。

设置：`ymz.executablePath`、`ymz.home`（覆盖 `YMZ_HOME`）、`ymz.useTerminal`、`ymz.preferredLocation`（`panel` \| `sidebar`）。

## 安装

**从 GitHub Release**（发版后；资产名 `ymz-vscode_{version}.vsix`，`{version}` 无前导 `v`）：

```bash
# example tag v0.7.0 → ymz-vscode_0.7.0.vsix
curl -fsSL -o ymz-vscode.vsix \
  "https://github.com/yyZe0122/YunmengZe-Agent/releases/download/v0.7.0/ymz-vscode_0.7.0.vsix"
code --install-extension ymz-vscode.vsix
```

Cursor / VSCodium：命令面板 → **Extensions: Install from VSIX…**

**从源码（本机）：**

```bash
make vscode
code --install-extension extensions/vscode/ymz-vscode_0.0.1.vsix
```

需要 Node 18+。开发：打开 **`extensions/vscode` 文件夹**（不要从仓库根 F5），然后 F5。

## 发版如何挂上 VSIX

不写进 `.goreleaser.yaml` 的 `extra_files`（`goreleaser release --clean` 会清空 `dist/`）。

唯一发版脚本 [`scripts/publish-release.sh`](../../scripts/publish-release.sh) 在 Go 资产上传成功之后：

1. `scripts/package-vscode.sh vX.Y.Z` → `extensions/vscode/ymz-vscode_{version}.vsix`（把扩展 `package.json` 的 version 戳成 tag，不提交；`dist/` 可写时再拷一份）
2. `gh release upload` 挂到同一 Release（资产名仍是 `ymz-vscode_{version}.vsix`）

GitHub Actions [`.github/workflows/release.yml`](../../.github/workflows/release.yml) 同样在 goreleaser 之后打包装上。

每个 tag **必须**挂上 VSIX。`package-vscode.sh` 失败、找不到 `ymz-vscode_{version}.vsix`、或 `gh release upload` 失败 → **整次发版失败**（需要 Node 18+；root 发版会 `su` 到仓库属主跑打包）。Go 二进制可能已经上传；修好 Node 后 `--upload-only`。不要另写 `scripts/release-*.sh`。

## 不做

Marketplace / Open VSX、内嵌 `ymzd`、给 TUI 加 `--port`、扩展里执行 tool、inline diff Accept/Reject、把二进制 POST 进 Gateway。
