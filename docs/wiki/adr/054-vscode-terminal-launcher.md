# ADR-054：VS Code 终端启动器（IDE 客户端边界）

- 状态：Accepted
- 日期：2026-08-24
- 更新：2026-08-24（V3：GitHub Release 挂 `ymz-vscode_{version}.vsix`）
- 更新：2026-09-04（V4 Webview 聊天见 [ADR-056](056-vscode-webview-chat.md)；本 ADR 的启动器降为 `ymz.useTerminal` 回退）
- 更新：2026-09-07（命令表：`openNewTerminal` 始终新开；Alt+K 打 Webview；启动器无默认键）

## 背景

YunmengZe 的主 UX 是本机 TUI（ADR-018 / 053）。VS Code / Cursor 里没有官方入口，现成第三方插件接不上本地 Gateway。完整 Claude Code 级侧栏（Webview 聊天、inline diff、多会话 tab）是另一条产品线，不是现在的缺口。现在的缺口是：**在编辑器里打开已经存在的 `ymz` TUI。**

OpenCode 的 `sdks/vscode` 是同档做法：扩展创建集成终端并启动 CLI。他们额外给 TUI 开了 `--port` + `POST /tui/append-prompt` 以便插入 `@file`。ymz TUI **没有**该控制面；本相不为此给 Go 加 HTTP。

## 决策

IDE 扩展是 **第三 peer**，本相 **只启动 TUI**：

```text
VS Code 扩展
  → 解析 ymz 可执行文件
  → 集成终端 sendText(ymz)     # 无参 = TUI；TUI 自己 Ensure daemon（ADR-037）
  → 可选 sendText(@file#L, false)  # 写入 PTY，不回车
```

- 源码在本仓 `extensions/vscode/`，**不**进入 `go.mod`。`make check` 不跑 npm。
- **启动器路径** **不** 读 `gateway.json`、**不** 调 `/v1/*`。Webview 聊天（ADR-056）才打 Gateway。两边都 **不** 打开 `core.db`、**不** 执行 tool / provider / grant。
- 关编辑器 **不** `ymz stop`（ADR-037）。
- **不** 把 `ymzd` / `ymz` 打进 VSIX。
- **不** 给 TUI 增加 `--port` / `append-prompt`。`@file` 用 VS Code `Terminal.sendText(ref, false)`。Charm AltScreen 或 perm 卡打开时，键可能打到错误焦点——可接受。
- 解析顺序：`PATH` 中的 `ymz`（Windows `ymz.exe`）→ `~/.local/bin/ymz`（`make install` 默认；Windows `%USERPROFILE%\.local\bin\ymz.exe`）。都找不到则 `showErrorMessage`，不向终端发送命令。
- 终端名固定 `ymz`。`openTerminal` 复用已有同名终端；`openNewTerminal` 始终 `ViewColumn.Beside` 新开。
- 分发：本机 `make vscode`；发版由 `publish-release.sh` / `release.yml` 在 goreleaser **之后** 打 `ymz-vscode_{version}.vsix` 并 `gh release upload`（不要写入 goreleaser `extra_files`：`--clean` 会清 `dist/`）。无 Node 则警告跳过，Go 资产仍成功。不上 Marketplace / Open VSX。用户说明：[`docs/wiki/vscode.md`](../vscode.md)。

原生 Webview 聊天已落地为 [ADR-056](056-vscode-webview-chat.md)（活动栏列表 + 编辑器 Tab → `/v1/*`）。本 ADR 的启动器保留为 `ymz.useTerminal` 回退；仍禁止给 TUI 加 `--port` / `append-prompt`。

## 命令

| 命令 | 默认快捷键 | 行为 |
| --- | --- | --- |
| `ymz.openTerminal` | 无（`ymz.useTerminal` 时 Ctrl/Cmd+Esc） | 已有名为 `ymz` 的终端则 show；否则分屏新建并启动 |
| `ymz.openNewTerminal` | 无（`ymz.useTerminal` 时 Ctrl/Cmd+Shift+Esc） | 始终 `ViewColumn.Beside` 新开一列 |
| `ymz.addFilepathToTerminal` | 无默认键 | 活动编辑器 `@path` / `#L` 写入名为 `ymz` 的终端（不回车） |

默认快捷键打 Webview（ADR-056）。`useTerminal` 时 Esc reuse、Shift+Esc 新开。

## 非目标

- 给 TUI 加 `--port` / `append-prompt`（Webview 主循环见 ADR-056）
- inline diff、Plan 文档批注、检查点 UI
- Marketplace publisher、Open VSX、bundled 二进制
- 扩展 import `internal/gateway` 或复制一份 daemon

## 后果

- TUI 仍是终端主 UX；本 ADR 的启动器是快捷方式。Webview 是 IDE 主 UX（ADR-056）。
- Webview 聊天见 [ADR-056](056-vscode-webview-chat.md)。Marketplace（V5）见 `docs/backlog/current.md`。安装/挂包步骤见 [`docs/wiki/vscode.md`](../vscode.md)。
