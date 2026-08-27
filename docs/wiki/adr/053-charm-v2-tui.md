# ADR-053：Charm v2 TUI

- 状态：Accepted
- 日期：2026-08-20
- 更新：2026-08-27（Shift+PgUp/Dn 切会话；`/edit` · `/editundo`）

## 背景

TUI 已对齐 Crush **契约**（slash、perm SSE、steer、折叠、T8 live MD），画面仍是 Bubble Tea v1。产品要求 Charm v2 引擎，画面用 YunmengZe 身份，不抄 Crush chrome（Crush FSL；本仓库 Apache-2.0）。

## 决策

`internal/tui` 迁到 Charm v2。分区用 lipgloss 色块拼接，不上 Ultraviolet 整页 ScreenBuffer，不自写 lazy list。仍是 **一个包**（ADR-001），Gateway-only（ADR-018）。

### 依赖

- `charm.land/bubbletea/v2`
- `charm.land/bubbles/v2`（textarea、key）
- `charm.land/lipgloss/v2`
- `charm.land/glamour/v2`
- `github.com/charmbracelet/ultraviolet` 仅为 v2 传递依赖；chrome **不用** 整页 ScreenBuffer

去掉 Bubble Tea / Lip Gloss / Glamour / Bubbles **v1**。

### 渲染

`View() tea.View`：`AltScreen = true`；**不设** `MouseMode`。分区：`header` / `main` / `pills` / `editor` / `status` / 宽屏 `sidebar`（空会话不画）。slash 补全盖在对话底部（不改 viewport 高度）。`textarea.SetVirtualCursor(false)`，`View.Cursor` 跟输入框偏移（IME 候选框跟 caret）。streaming 仍 T8。chrome 为清宣纸（少框、无 `░▒`）；无块字字标；毛笔仅 landing。

### 身份

- 色盘：青绿山水夜 / 宣纸昼。非 Crush mist teal
- header 高 2（1 行 + 底细线）：`ymz · 版本 · 会话名 · sse`。`0.0.0-dev` 显示 `dev`。毛笔只在 landing
- editor 印旁常驻当前模型；status 在输入框下方
- Tab / Shift+Tab：**agent → plan → auto**（编辑器焦点；Shift+Tab 反向）
- 权限：once / similar / permanent / deny（ADR-043）
- 运行中 Enter = steer（ADR-052）；空闲回车提交新一轮；steer 409 回退 submit
- Ctrl+C 清输入；`/quit` 才退出

### 焦点

编辑器始终聚焦。PgUp/PgDn 滚 **当前** 对话。Shift+PgUp / Shift+PgDn 环形切会话。空输入时 e/E/c 折叠。Esc 链（overlay 先关）：running → cancel；否则 800ms 内再 Esc = `/undo`。`/edit` 隐藏上一轮并填入编辑器；`/editundo` 同时撤回该轮文件（rewind 失败则不 hide）。running 状态行用 spinner；已完成气泡不因 Anim 重绘。

### 快捷键

| 键 | 行为 |
| --- | --- |
| Ctrl+P | 命令盘 |
| Ctrl+L | 模型盘 |
| Ctrl+S | 会话盘 |
| Ctrl+T | pills 展开/收起 |
| Shift+PgUp / Shift+PgDn | 更旧 / 更新会话 |
| Shift+Enter / Ctrl+J | textarea 换行 |

### Gateway

只读 `GET /v1/sessions/{id}/todos`（`corequery.ListSessionTodos`）。TUI 不 import `sessiontodo`。写仍只经 `todo_write`。不做 Crush 式 next-turn 队列 pill。

### 不做

鼠标抓取、yolo、LSP、图片附件、Kitty graphics、Crush 三档 perm、TUI import tools/agent/chatsession/sqlite。

## 后果

- Charm v2 已迁。画面不对标 Crush chrome。
- 同包再拆（`raster.go` / `paths.go` / `mascot.go` / `panel.go` / `view_*`），不新建包。
