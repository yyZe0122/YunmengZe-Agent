# ADR-018：本地 Gateway 与 CLI 边界

- 状态：Accepted
- 日期：2026-07-14
- 更新：2026-08-27（`POST /v1/sessions/{id}/retract`；hidden task 不进 transcript/ListTasks）
- 更新：2026-08-24（ADR-054：VS Code 终端启动器是第三 peer，本相不走 Gateway HTTP）
- 更新：2026-09-04（ADR-056：VS Code Webview 是第四 peer，走 `/v1/*`；启动器仍不调 Gateway）

## 决策

Gateway 是 `ymzd` 的本地控制面。`ymz` 的 **CLI 子命令**与 **TUI** 都只是它的客户端，经 `internal/gatewayclient` 访问 Gateway；二者并列，TUI **不** exec CLI。**TUI 为主产品路径**，CLI 为脚本/自动化次要入口。Gateway 不执行 Tool、不直接调用 Provider，也不能发行 Grant。

Linux/macOS 在 RuntimeDir 使用受文件权限保护的 Unix Domain Socket；Windows 使用仅监听 loopback 的随机端口和随机 Bearer Token。endpoint、Token 或 Socket 路径通过受限 discovery 文件发布。

当前 API（路径以代码为准）：

| Method | Path | 说明 |
| --- | --- | --- |
| `GET` | `/v1/health` | daemon 健康 |
| `GET`/`PUT` | `/v1/config/model` | 无密钥；可选 `context_window`；PUT 热切换顶层 `model` |
| `GET` | `/v1/config/mcp` | MCP 状态（无 URL/headers；ADR-040） |
| `GET` | `/v1/config/commands` | `chat.commands`（id/description/template） |
| `GET` | `/v1/skills` | Catalog；`include_archived`；draft/last_used/archived |
| `GET` | `/v1/skills/events` | `limit` · `skill_id` |
| `POST` | `/v1/skills/actions` | apply\|reject（ADR-050） |
| `GET` | `/v1/memory` | `session_id` · `q` · `kind` · `limit` · `offset` · `include_global` · `include_archived` |
| `POST` | `/v1/memory/actions` | refresh\|forget\|promote |
| `GET` | `/v1/sessions` · `/v1/sessions/{id}` | 列表 / 读 |
| `PATCH` | `/v1/sessions/{id}` | `{preferred_model}`（O4；空串清除）和/或 `{permission_stance}`（agent\|auto\|plan） |
| `GET` | `/v1/sessions/{id}/messages` | transcript（跳过 `hidden_task_ids`） |
| `GET` | `/v1/sessions/{id}/todos` | session todos 只读（ADR-053 pills） |
| `GET` | `/v1/sessions/{id}/context` | 窗压（ADR-041） |
| `POST` | `/v1/sessions/{id}/compact` | `{focus?}`；TUI `/compact` |
| `POST` | `/v1/sessions/{id}/rewind` | 人径撤回上次 agent 写文件（QG；TUI `/undo` · Esc Esc） |
| `POST` | `/v1/sessions/{id}/retract` | `{rewind_files?}`；先停写再 rewind（可选）再隐藏上一轮（TUI `/edit` / `/editundo`）。rewind 失败不 hide。成功后清该轮 `transcript_search`、会话 todos、盖住该轮的 compaction。`agent_run_records` 仍保留。 |
| `POST` | `/v1/sessions/{id}/steer` | `{text}`；运行中下一 step（ADR-052 R3） |
| `GET` | `/v1/questions` | pending `ask_user`（`session_id` · `limit`） |
| `POST` | `/v1/questions/{id}/answer` | `{answers, actor}`（ADR-052 R4） |
| `POST` | `/v1/questions/{id}/dismiss` | `{actor}`；整组 unavailable（Esc Esc） |
| `GET`/`POST` | `/v1/tasks` | 列表 / 提交（`execution_mode` · `permission_stance` · `preferred_model` · `interactive` · `skill_ids` · `workspace`）；列表跳过 hidden |
| `GET` | `/v1/tasks/{id}` | 读（hidden → not found） |
| `POST` | `/v1/tasks/{id}/actions` | pause\|resume\|cancel + `expected_version` |
| `GET` | `/v1/tasks/{id}/usage` · `/context` · `/messages` | 用量 / 窗压 / transcript |
| `GET` | `/v1/permissions` | `session_id` · `limit` |
| `POST` | `/v1/permissions/{id}/decide` | once\|similar\|permanent\|deny（ADR-043/046；≠ 整单审批） |
| `GET`/`POST` | `/v1/jobs` | 列表（`include_archived`）/ 创建 |
| `GET` | `/v1/jobs/{id}` | 读 |
| `POST` | `/v1/jobs/{id}/actions` | pause\|resume\|cancel（cancel→archived；ADR-042） |
| `GET` | `/v1/plans` · `/v1/plans/{id}` | 只读 |
| `GET` | `/v1/approvals` | 只读历史系统审批 |
| `GET` | `/v1/runs` · `/v1/runs/{id}` · `/v1/runs/{id}/usage` | 列表无 `task_id` 过滤；跳过 hidden task 的 run |
| `GET` | `/v1/events` · `/v1/events/stream` | 查询 + SSE（含 `permission.*` · `question.*`） |
| `GET` | `/v1/model-stream` | SSE `event: model` + Envelope（ADR-031） |

Chat run 解析：**job pin → session prefer → main**（ADR-045）。TUI SSE 触发 permission / question 刷新；decide / answer 仍走 HTTP。

**不得恢复：** 人批 prompt/decide、plan-step `POST /v1/runs`、`/v1/modules`。

Gateway 不持有通用 `*sql.DB` 业务能力。只读查询进入 `internal/corequery.Store`；Task 提交进入 `tasksubmission.Service`；agent/plan chat 进入 `chatsession`；pause/resume/cancel 进入 `taskcontrol`；Job 进入进程内 `scheduler.Store`（fire 由 `scheduledtasks` 桥接）。

任何从 API 触发的模型工具调用最终仍必须进入 Tool Broker。chat 路径上的 grant 由 `chatsession` 按 mode 签发。

## 客户端包边界

```text
cmd/ymz          flag / daemon ensure / 子命令与 tui.Run 入口
internal/tui             Charm v2 TUI（ADR-053）；消费窄 `tui.Gateway`（由 gatewayclient 满足）；主 UX
                          分发：`cmds.go` + `cmds_{session,skills,memory,perm,cron,model,refresh}.go`
                          Elm：`update.go` + `update_{refresh,stream,keys}.go`
                          表现：`View() tea.View` + lipgloss 色块分区（无 UV 整页 / lazy list）；textarea；picker overlay；
                          完成态 glamour v2；streaming 冻结前缀 glamour + trail 永远 plain（T8）；
                          折叠 e/E/c（thinking / tool 默认折；**live 回复不折**）；**不设 MouseMode**（终端原生划词）；
                          mid-turn refresh 保留 typewriter；吸底 pin；Tab = agent/plan/auto
internal/gatewayclient   共享 HTTP/SSE 外观 + transport（不 import gateway server）
internal/gateway         服务端 only：路由 / handlers / LocalRunner
extensions/vscode        VS Code 扩展：Webview 聊天（ADR-056）打 `/v1/*`；终端启动器（ADR-054）仍不调 Gateway
```

TUI 与 CLI 不得 import `tools`、`providers`、`store/sqlite`、`agent`、`chatsession` 实现。主交互斜杠：`/new`（离焦 ready，运行中则 cancel）、`/edit`（隐藏上一轮并填编辑器）、`/editundo`（同上并撤回该轮文件）、`/undo`、`/cron`、`/compact`、`/perm`、`/memory`、`/expand`、`/journey`（memory + skill 事件）、`/skills`（含 apply/reject/archived；显式预载快照）、`/<skill-id>`、`/<command>`（`chat.commands`）、`/model`（本会话）/ `/model main`（全局默认）、`/status`（含 daemon 版本）。运行中普通回车 = steer（不 cancel）；空闲/已结束会话回车提交新一轮；steer 409 回退 submit。提问/授权卡开着时 Enter 不 steer。`ask_user` 弹出问题卡（perm 优先；自定义回答；Esc Esc dismiss）。折叠快捷键：`e` / `E` / `c`（输入为空时）。用户规则：`<ConfigDir>/AGENTS.md` + 可选项目 `.yunmengze/AGENTS.md`。Prefix 含技能目录；正文仍 `skill_view`。CLI：`ymz config import-opencode`（离线写 ConfigDir，不经 Gateway）。可选尾巴见 `docs/backlog/current.md`。
