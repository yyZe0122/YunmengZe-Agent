# ADR-053：Charm v2 TUI（Crush-feel chrome）

- 状态：Accepted
- 日期：2026-08-20
- 更新：2026-08-24（无独立滚动焦点；Esc Esc undo；Ctrl+PgUp/PgDn 切会话；chrome 焦墨色块）

## 背景

TUI 已对齐 Crush **契约**（slash、perm SSE、steer、折叠、T8 live MD），画面仍是 Bubble Tea v1 + `viewport` 整串字符串。Crush 的跟手感来自 `charm.land/bubbletea/v2`、Ultraviolet 矩形画布、自定义 lazy list、textarea、dialog 栈和工具专用渲染。backlog 原先禁止换引擎；产品要求贴 Crush 手感并保留 YunmengZe 身份。

## 决策

`internal/tui` 迁到 Charm v2（bubbletea / bubbles / lipgloss / glamour v2）。分区用 lipgloss 色块拼接，不上 Ultraviolet 整页 ScreenBuffer，不自写 lazy list。仍是 **一个包**（ADR-001），Gateway-only（ADR-018）。不拷贝 Crush 源码（Crush FSL；本仓库 Apache-2.0）。

### 依赖

- `charm.land/bubbletea/v2`
- `charm.land/bubbles/v2`（textarea、spinner、key）
- `charm.land/lipgloss/v2`
- `charm.land/glamour/v2`
- `github.com/charmbracelet/ultraviolet` 仅为 v2 传递依赖；chrome **不用** 整页 ScreenBuffer

去掉 Bubble Tea / Lip Gloss / Glamour / Bubbles **v1**。

### 渲染

`View() tea.View`：`AltScreen = true`；**不设** `MouseMode`（默认 none）。终端原生划词复制保留。分区：`header` / `main` / `pills` / `editor` / `status` / 宽屏 `sidebar`（空会话不画）。streaming 仍 T8。chrome 为焦墨直角色块（见 backlog **TUI-ink**）。

### 身份（不变）

- 色盘：焦墨夜 / 宣纸昼（朱砂印；非 mist teal）
- Tab / Shift+Tab：**plan → agent → auto**（编辑器焦点）
- 权限：once / similar / permanent / deny（ADR-043）
- 运行中 Enter = steer（ADR-052）
- Ctrl+C 清输入；`/quit` 才退出

### 焦点

编辑器始终聚焦。PgUp/PgDn 滚 **当前** 对话（picker 不抢）。Ctrl+PgUp / Ctrl+PgDn 在 `ListSessions`（updated_at DESC）里环形切会话：PgUp=更旧，PgDn=更新；landing 从端点进。空输入时 e/E/c 折叠。不另设滚动焦点、不实现空格折叠、Ctrl+O `$EDITOR`。

Esc 链（overlay 先关）：running → cancel；否则 800ms 内再 Esc = `/undo`（`/undo` 命令仍在）；第一次 Esc 清输入并记时。

### 快捷键（新增 / 对齐 Crush chrome）

| 键 | 行为 |
| --- | --- |
| Ctrl+P | 命令盘 |
| Ctrl+L | 模型盘 |
| Ctrl+S | 会话盘 |
| Ctrl+T | pills 展开/收起 |
| Ctrl+PgUp / Ctrl+PgDn | 更旧 / 更新会话 |
| Shift+Enter / Ctrl+J | textarea 换行 |

### Gateway

只读 `GET /v1/sessions/{id}/todos`（`corequery.ListSessionTodos`）。TUI 不 import `sessiontodo`。写仍只经 `todo_write`。不做 Crush 式 next-turn 队列 pill（ADR-052 无 next-turn inbox）。

### 不做

鼠标抓取、yolo、LSP、图片附件、Kitty graphics、Crush 三档 perm、TUI import tools/agent/chatsession/sqlite。

## 后果

- Charm v2 已迁。画面不对标 Crush chrome；活方案在 `docs/backlog/current.md` **Phase TUI-ink**。
- 同包再拆（`logo.go` / `panel.go` / `view_*`），不新建包。
