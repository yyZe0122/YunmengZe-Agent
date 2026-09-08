# ADR-054：VS Code 终端启动器（IDE 客户端边界）

- 状态：Accepted
- 日期：2026-08-24
- 更新：2026-08-24（V3：GitHub Release 挂 `ymz-vscode_{version}.vsix`）
- 更新：2026-09-04（V4 Webview 聊天见 [ADR-056](056-vscode-webview-chat.md)；本 ADR 的启动器曾降为 `ymz.useTerminal` 回退）
- 更新：2026-09-07（命令表：`openNewTerminal` 始终新开；Alt+K 打 Webview；启动器无默认键）
- 更新：2026-09-07（独立 TUI VSIX `ymz-vscode-tui`；终端 `cwd` = VS Code 文件夹；去掉 `useTerminal`）

## 背景

YunmengZe 的主 UX 是本机 TUI（ADR-018 / 053）。VS Code / Cursor 里没有官方入口，现成第三方插件接不上本地 Gateway。完整 Claude Code 级侧栏（Webview 聊天、inline diff、多会话 tab）是另一条产品线，不是现在的缺口。现在的缺口是：**在编辑器里打开已经存在的 `ymz` TUI。**

OpenCode 的 `sdks/vscode` 是同档做法：扩展创建集成终端并启动 CLI。他们额外给 TUI 开了 `--port` + `POST /tui/append-prompt` 以便插入 `@file`。ymz TUI **没有**该控制面；本相不为此给 Go 加 HTTP。

## 决策

TUI 启动器是 **独立 VSIX**（`yunmengze.ymz-vscode-tui` / `ymz-vscode-tui_{version}.vsix`），与 Webview GUI（ADR-056）并列发版。**不要两包同装**（Esc 冲突）。源码仍在 `extensions/vscode/`（`src/extension-tui.ts` + `package-tui.json`），**不**进入 `go.mod`。`make check` 不跑 npm。

```text
VS Code TUI 扩展
  → 解析 ymz 可执行文件
  → 集成终端 cwd = workspaceFolders[0]
  → sendText(ymz)                 # 无参 = TUI；TUI 自己 Ensure daemon（ADR-037）
  → 可选 sendText(@file#L, false)  # 写入 PTY，不回车
```

- **启动器路径** **不** 读 `gateway.json`、**不** 调 `/v1/*`。Webview 聊天（ADR-056）才打 Gateway。两边都 **不** 打开 `core.db`、**不** 执行 tool / provider / grant。
- 关编辑器 **不** `ymz stop`（ADR-037）。
- **不** 把 `ymzd` / `ymz` 打进 VSIX。
- **不** 给 TUI 增加 `--port` / `append-prompt`。`@file` 用 VS Code `Terminal.sendText(ref, false)`。Charm AltScreen 或 perm 卡打开时，键可能打到错误焦点——可接受。
- 解析顺序：`PATH` 中的 `ymz`（Windows `ymz.exe`）→ `~/.local/bin/ymz`（`make install` 默认；Windows `%USERPROFILE%\.local\bin\ymz.exe`）。都找不到则 `showErrorMessage`，不向终端发送命令。
- 终端名固定 `ymz`。`cwd` = 当前 VS Code 文件夹，使 Charm `os.Getwd()` / 会话 `workspace` 与 GUI 对齐（ADR-046）。`open` 复用已有同名终端；`openNew` 始终 `ViewColumn.Beside` 新开。
- 分发：本机 `make vscode` 打 GUI + TUI 两包；发版由 `publish-release.sh` / `release.yml` 在 goreleaser **之后** 打 `ymz-vscode_{version}.vsix` **和** `ymz-vscode-tui_{version}.vsix` 并 `gh release upload`（不要写入 goreleaser `extra_files`：`--clean` 会清 `dist/`）。缺任一包 **失败**。不上 Marketplace / Open VSX。用户说明：[`docs/wiki/vscode.md`](../vscode.md)。

原生 Webview 聊天见 [ADR-056](056-vscode-webview-chat.md)。GUI **不再** 提供 `ymz.useTerminal` 回退。仍禁止给 TUI 加 `--port` / `append-prompt` / 鼠标拖放。

## 命令

| 命令 | 默认快捷键 | 行为 |
| --- | --- | --- |
| `ymz.tui.open` | Ctrl/Cmd+Esc | 已有名为 `ymz` 的终端则 show；否则分屏新建并启动 |
| `ymz.tui.openNew` | Ctrl/Cmd+Shift+Esc | 始终 `ViewColumn.Beside` 新开一列 |
| `ymz.tui.addFilepath` | Alt/Option+K（编辑器焦点） | 活动编辑器 `@path` / `#L` 写入名为 `ymz` 的终端（不回车） |
| `ymz.tui.addExplorerPath` | 无 | 资源管理器路径写入名为 `ymz` 的终端（不回车） |

## 非目标

- 给 TUI 加 `--port` / `append-prompt`（Webview 主循环见 ADR-056）
- TUI 拖文件 / 鼠标抓取 / Kitty graphics
- inline diff、Plan 文档批注、检查点 UI
- Marketplace publisher、Open VSX、bundled 二进制
- 扩展 import `internal/gateway` 或复制一份 daemon

## 后果

- TUI 仍是终端主 UX；本 ADR 的启动器是独立 IDE 入口。Webview 是另一张皮（ADR-056）。
- Webview 聊天见 [ADR-056](056-vscode-webview-chat.md)。Marketplace（V5）见 `docs/backlog/current.md`。安装/挂包步骤见 [`docs/wiki/vscode.md`](../vscode.md)。
