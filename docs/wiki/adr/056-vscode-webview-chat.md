# ADR-056：VS Code Webview 聊天（第四 peer）

- 状态：Accepted
- 日期：2026-09-04
- 更新：2026-09-07（Esc = focus composer；点聊天才 `ymz start`；`/perm` grant 配套 Go）
- 更新：2026-09-07（Tab 切 agent/plan/auto；权限卡浮层 + 2s poll；Esc Esc deny）
- 更新：2026-09-07（去掉 `useTerminal`；静默 `ymz start`；TUI 独立 VSIX 见 ADR-054）

## 背景

ADR-054 的终端启动器接不住拖文件，也不是 Claude Code 式 GUI。TUI 是 Charm 进程内 Elm 循环，没有 HTTP/IPC；给 TUI 加 `--port` / `append-prompt` 会变成第二控制面，已在 ADR-054 拒绝。

V4 要一张 **GUI 皮**：活动栏会话列表 + 可拖动的编辑器 Tab，能力对齐本机 TUI，走现有 Gateway `/v1/*`。TUI 入口另打 `ymz-vscode-tui`（ADR-054），GUI 不再内嵌 `useTerminal` 回退。

## 决策

扩展是 **第四 peer**，与 CLI / TUI 并列打本地 Gateway。Webview **不**跑 tool / provider / grant。聊天路径上的 `/perm similar|permanent` 需要 daemon 能签发收窄 grant：`internal/approval` command-narrowing + `internal/toolpermission` 把 grant TTL 夹进 chat system approval 的 24h 窗（否则 IssueGrant 拒签，HTTP 500）。这是 V4 配套，不是第二控制面。

```text
ymzd Gateway /v1/*
  ├─ TUI     gatewayclient
  ├─ CLI     gatewayclient
  └─ VS Code 扩展 Host 读 gateway.json → HTTP/UDS
       Webview 无 Node、无 token；全部 /v1 经 postMessage 代理
```

### 产品形状（对标 Claude Code 插件）

- 活动栏：会话列表（始终可见；未连上时占位，不拉 daemon）。
- 对话：`WebviewPanel` 编辑器 Tab；用户可拖到左/右侧栏。
- Ctrl/Cmd+Esc：已有 Tab 则 **focus 输入框**；否则新 Tab。不是编辑器 ↔ 输入框切换。
- Ctrl/Cmd+Shift+Esc：始终新会话 Tab。
- Alt/Option+K：当前文件/选区 `@path` / `#L` 写入**当前会话输入框**。
- 关编辑器 **不** `ymz stop`。
- TUI 启动器是独立 VSIX（ADR-054）。本扩展 **不** 提供 `ymz.useTerminal`。

### 发现与 ensure

- user mode：`YMZ_HOME` 或设置 `ymz.home`，否则 `~/.yunmengze/run/gateway.json`。
- Unix：Node `http.request({ socketPath })`（socket 必须落在 runtime dir 内；不给 Gateway 开 TCP）。
- Windows：loopback + Bearer（非 loopback / 无 token 拒绝）。
- **activate 不** `ymz start`。点聊天 Tab / 会话 / Focus Input / 拖文件 才 `ensureGateway`：health 失败则 **静默** `spawn(ymz start)`（`cwd` = 当前文件夹，`stdio: ignore`，`windowsHide: true`），轮询 `/v1/health`。找不到 `ymz` 则报错，**不**向终端发命令、**不**弹控制台。连上后再刷会话列表。

提交 `workspace` = 当前工作区根（ADR-046）。`interactive: true`。permission/question `actor` = `vscode`。`/cron` 与 TUI 同字段：Go duration、`skill_ids`、`model_ref`（session prefer 否则 main）。

**Tab / Shift+Tab** 只循环 `agent → plan → auto`（slash 面板与权限/提问卡打开时也一样；补全用点击）。内核仍 `execution_mode=agent|plan`；Auto = session `permission_stance`。

权限卡浮在时间线与 composer 之间（不跟消息滚走）。SSE `permission.*` 即时刷新，Host 另有 2s poll（对标 TUI）。`/perm once|similar|permanent|deny <id-prefix>` 走 decide。权限卡 Esc Esc（3s）= **deny**（与 TUI / ADR-043 相同）；提问卡 Esc Esc（3s）才 dismiss。取消等待中的 tool call 时 daemon 给 in-flight 以及同 step 未执行 sibling 写入 `{"error":"interrupted"}`，packing 不再补 `orphan_tool_call`（ADR-052）。

### 拖文件

工作区内 → `@rel/path`（选区则 `#L12` / `#L12-20`）。工作区外 → `@/abs/path`（不拷贝、不上传）。图/视频同样只插路径；主循环仍文本。agent 读工作区外路径走现成 extra-root `/perm` 四档。

斜杠优先级与 TUI 相同：内置 → `chat.commands` → skill id。不要把 `/perm` 当用户消息发出。

### 不做（本轨）

- 调 TUI / 给 TUI 加端口 / exec 斜杠当 API / `useTerminal` 回退
- 扩展进程跑 tool / provider / grant
- 二进制 POST 进 Gateway；主循环多模态
- 编辑器 inline diff / Accept·Reject（无 proposed-edit 合同）
- Marketplace / Open VSX / 把 `ymz` 打进 VSIX
- session groups、云端会话、Remote Control、Focus view
- 开 IDE 即拉起 daemon
- 为拉 daemon 开终端或弹控制台

## 后果

- TUI 仍是终端主 UX（独立 VSIX，ADR-054）；本扩展是 IDE GUI。
- 安装/挂包仍见 [`docs/wiki/vscode.md`](../vscode.md)。活尾巴 Marketplace = V5，见 backlog。
