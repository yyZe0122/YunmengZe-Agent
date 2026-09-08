# VS Code / Cursor 扩展

用户安装与发版挂包。架构：[ADR-056](adr/056-vscode-webview-chat.md)（Webview GUI）· [ADR-054](adr/054-vscode-terminal-launcher.md)（TUI 启动器，独立 VSIX）。Marketplace 仍只在 [`docs/backlog/current.md`](../backlog/current.md)。

源码：[`extensions/vscode/`](../../extensions/vscode/)。扩展 **不** 进 `go.mod`；`make check` 不跑 npm。

每个 tag 打 **两个** VSIX，**不要同时安装**（快捷键冲突）：

| 资产 | 扩展 id | UX |
| --- | --- | --- |
| `ymz-vscode_{version}.vsix` | `yunmengze.ymz-vscode` | Webview 聊天（活动栏 + 编辑器 Tab） |
| `ymz-vscode-tui_{version}.vsix` | `yunmengze.ymz-vscode-tui` | 集成终端启动 TUI |

两边会话目录都是 **当前 VS Code 文件夹**（`workspaceFolders[0]`）。GUI 提交 `workspace`；TUI 终端 `cwd` 相同，Charm `os.Getwd()` 对齐。

需要本机已有 `ymz`（`PATH`、`~/.local/bin/ymz`，或设置 `ymz.executablePath`）。找不到会报错。关编辑器 **不停** daemon（`ymz stop`）。

## GUI（`ymz-vscode`）

Claude Code 式本机 GUI：活动栏会话列表 + 可拖到侧栏的编辑器 Tab。走本地 Gateway `/v1/*`（`interactive: true`）。扩展 **不** 跑 tool / provider，**不** 把 `ymzd` 打进 VSIX。

**activate 不拉 daemon**；第一次开聊天 Tab / Focus Input / 拖文件才静默 `ymz start`（`windowsHide`，不开终端）。

| 命令 | 快捷键 | 行为 |
| --- | --- | --- |
| YunmengZe: Focus Input | Ctrl/Cmd+Esc | 已有聊天 Tab 则 focus 输入框；否则开新 Tab |
| YunmengZe: Open in New Tab | Ctrl/Cmd+Shift+Esc | 始终新会话 Tab（编辑器标题栏同一按钮） |
| YunmengZe: Insert @-file reference | Alt+K / Option+K | 当前文件/选区写入输入框：`@path` / `@path#L12` / `@path#L12-20` |

拖工作区文件进输入框 → `@rel/path`。拖工作区外图片/视频/文件 → `@/abs/path`（不拷贝）；agent 读盘时走 extra-root `/perm`。

聊天内：**Tab / Shift+Tab** 循环 agent → plan → auto（slash 列表不抢 Tab）。区外路径的 `/perm` 卡浮在输入框上方（once / similar / permanent / deny）；Esc Esc（3s）deny。`/perm once|similar|permanent|deny <id>` 可从输入框决策。

设置：`ymz.executablePath`、`ymz.home`（覆盖 `YMZ_HOME`）、`ymz.preferredLocation`（`panel` \| `sidebar`）。

## TUI（`ymz-vscode-tui`）

只启动已有的 `ymz` TUI（ADR-054）。**不** 读 `gateway.json`、**不** 调 `/v1/*`。终端名固定 `ymz`，`cwd` = 当前文件夹。TUI 自己 Ensure daemon。无拖文件芯片；资源管理器 / Alt+K 把 `@path` 打进终端（不回车）。

| 命令 | 快捷键 | 行为 |
| --- | --- | --- |
| YunmengZe TUI: Open | Ctrl/Cmd+Esc | 已有名为 `ymz` 的终端则 show；否则分屏新建并启动 |
| YunmengZe TUI: Open in new tab | Ctrl/Cmd+Shift+Esc | 始终 `ViewColumn.Beside` 新开 |
| YunmengZe TUI: Insert @-file | Alt+K / Option+K | 当前文件/选区 `@path` / `#L` 写入名为 `ymz` 的终端（不回车） |

TUI 不设鼠标抓取；不能把本机文件/视频拖进输入框。媒体仍走辅助工具（ADR-055）+ extra-root `/perm`。

## 安装

**从 GitHub Release**（发版后；`{version}` 无前导 `v`）。TUI 包后挂到同一 `v0.7.0` Release（changelog 正文仍是发版时的 GUI-only 说明）：

```bash
# GUI — v0.7.0
curl -fsSL -o ymz-vscode.vsix \
  "https://github.com/yyZe0122/YunmengZe-Agent/releases/download/v0.7.0/ymz-vscode_0.7.0.vsix"
code --install-extension ymz-vscode.vsix

# TUI — mutually exclusive with GUI; same tag, uploaded later
curl -fsSL -o ymz-vscode-tui.vsix \
  "https://github.com/yyZe0122/YunmengZe-Agent/releases/download/v0.7.0/ymz-vscode-tui_0.7.0.vsix"
code --install-extension ymz-vscode-tui.vsix
```

Cursor / VSCodium：命令面板 → **Extensions: Install from VSIX…**

**从源码（本机）：**

```bash
make vscode
code --install-extension extensions/vscode/ymz-vscode_0.0.1.vsix
# or
code --install-extension extensions/vscode/ymz-vscode-tui_0.0.1.vsix
```

需要 Node 18+。开发 GUI：打开 **`extensions/vscode` 文件夹**（不要从仓库根 F5），然后 F5。开发 TUI：`make vscode` 后装 `ymz-vscode-tui_*.vsix`（F5 只起 GUI）。`npm run vsix` 只打 GUI；两包走 `make vscode` / `scripts/package-vscode.sh`。

## 发版如何挂上 VSIX

不写进 `.goreleaser.yaml` 的 `extra_files`（`goreleaser release --clean` 会清空 `dist/`）。

唯一发版脚本 [`scripts/publish-release.sh`](../../scripts/publish-release.sh) 在 Go 资产上传成功之后：

1. `scripts/package-vscode.sh vX.Y.Z` → `ymz-vscode_{version}.vsix` + `ymz-vscode-tui_{version}.vsix`（把扩展 `package.json` 的 version 戳成 tag，不提交；`dist/` 可写时再拷一份）
2. `gh release upload` 挂到同一 Release（两包都必挂）

GitHub Actions [`.github/workflows/release.yml`](../../.github/workflows/release.yml) 同样在 goreleaser 之后打包装上。

每个 tag **必须**挂上 **两个** VSIX。`package-vscode.sh` 失败、找不到任一包、或 `gh release upload` 失败 → **整次发版失败**（需要 Node 18+；root 发版会 `su` 到仓库属主跑打包）。Go 二进制可能已经上传；修好 Node 后 `--upload-only`。不要另写 `scripts/release-*.sh`。

## 不做

Marketplace / Open VSX、内嵌 `ymzd`、给 TUI 加 `--port`、扩展里执行 tool、inline diff Accept/Reject、把二进制 POST 进 Gateway、GUI/TUI 同装。
