# ADR-039：逻辑子 Run（parent_run_id）

- 状态：Accepted（已实现首版）
- 日期：2026-07-31
- 更新：2026-09-03（显式 `task_id` 复用同一 child run；主 packing/TUI/`session_search` 排除 child records；resume 不反向改 `runs.state`，busy = 进程内 inflight）
- 更新：2026-08-28（Phase S+W：`task.kind` 目录；广告 `general`/`explore`/`web`；子 Run 永远叶子，剔 `task`）
- 更新：2026-08-17（ADR-052 R5：子代理 Prefix 继承 AGENTS.md；`http_get` 可随父 allowed_tools 继承，仍不预发）
- 更新：2026-08-11 可观测：`GET /v1/runs/{id}/usage` 父 Run self+children 上卷（T5）

## 背景

复杂任务需要委托子代理。生产边界禁止新 OS 进程、Module Runtime 或独立模块。ADR-001 已规定：子代理 = 带 `parent_run_id` 的逻辑子 Run，共享预算、授权与审计。

## 决策

### 触发方式

与 OpenCode 等 coding agent 一致：模型经 **Tool Broker** 调用内置工具 **`task`** 创建并同步等待子 Run。Gateway/API 不作为主路径；不新增旁路 agent 入口。

### 执行关系（首版）

**同步阻塞**：父 Run 在 `task` 工具返回前等待子 Run 终态；首版不并行多个子 Run。父 cancel / ctx 取消必须传播到子。

### 数据模型

- `runs.parent_run_id`：可空外键（逻辑引用父 Run ID）；新 migration 追加，不改历史 migration 文件。
- `runs.child_kind` / `runs.child_tools`（migration 029）：首次 spawn 写入，resume 校验。
- 子 Run 首次属于当时的父 Task；显式 `task_id` 续跑可改挂到当前父 Task / 当前父 Run（同一 session），**不**把 `runs.state` 从终态拉回 `running`。`SessionTranscript` / `SessionTranscriptTail` / `session_search` **排除** `parent_run_id IS NOT NULL`（写路径不索引 child；读路径再滤一层）。`TaskTranscript` 仍含 child（调试）。
- 深度上限：`max_depth`（默认 2）仍作保险；超限 fail-closed，工具返回错误文本，不创建 Run。子 Run **永远叶子**：解析后的 `allowed_tools` 不含 `task`，主模型不能靠深度再叠一层委派。

### 授权与预算

- Grant：子 Run **不得扩大**父的 workspace roots / 读写天花板；父为 plan（只读）则子只读。
- 预算：子消耗计入父 Task 用量聚合；子 Run 使用父的 `MaxTotalTokens` / cost 上限（chat 默认 128k 寿命帽），超限 fail-closed。`task_id` 续跑计入**该 child run 的历史 usage**（同一 run 寿命，不是本段增量）。续跑无独立 ContextView；过长会顶窗。
- 可观测（读路径）：`corequery.RunUsage(runID)` = 本 Run + 一层 `parent_run_id` 子 Run 的 assistant usage 汇总；Gateway `GET /v1/runs/{id}/usage`；TUI Metrics 在存在子 Run 时展示 parent/children 旁注。不改预算策略。
- 高风险工具：子 **不得** 自行扩大；仅当父 agent 已因 `chat.tools`（或其它合法 Grant）具备 `git_*` / `process_exec` / `process_shell` 时，子才可经 `allowed_tools ⊆ 父` 继承。`http_get` 若在父广告集中可继承，仍不预发（须 `/perm`）。子 Prefix 继承同一套 AGENTS.md，不继承父 Tail / 记忆 / 技能正文。
- 子 `allowed_tools` = kind 默认集 ∩ 父集 − kind 禁带 − `task`。空 `tools` 用 kind 默认（`general` = 父集 − 禁带）。显式请求了 kind 禁带的名字 → fail-closed，不建 Run。

### 工具 `task` 语义

输入：`prompt`（必填）、可选 `kind`、可选 `tools` 子集、可选 `task_id`。`kind` 省略 = `general`。schema enum 只列 **当前广告** 的 kind。`task_id` 省略 = 新建；传入 = 续跑同一 child `run_id`（句柄值 = 上次返回的 `task_id`/`run_id`）。仅主模型显式传入；不自动匹配、不列目录。

续跑 fail-closed：同 session、`parent_run_id` 非空；`kind`/`tools` 若传入必须与首次一致。`cancelled` / 跨 session / 父 run id → 观察错误，不建新 Run。进程内 inflight（本进程正在 `Run` 该句柄）→ `handle_busy`。DB 为 `running` 但本进程无 inflight = 崩溃孤儿，允许接管。`ResumePrompt` 在 restore 之后无条件追加（含 failed / 半截 tool 历史），不要求上次是最终 assistant。续跑用首次 Prepare 前缀 + `ResumePrompt`；工具集 = 存盘集 ∩ 当前父允许集（不得扩大）。pre-029 旧 child（空 `child_kind`/`child_tools`）不可 resume。

| kind | 广告（S） | Role | 默认工具 | 禁带（另：永远剔 `task`） |
| --- | --- | --- | --- | --- |
| `general` | 是 | `subagent`（缺则 main） | 父集 − 禁带；保留父已有的 `mcp_*` | `ask_user` `memory_write` `memory_promote` `skill_draft` |
| `explore` | 是 | `subagent` | 只读 fs + grep/glob + skills_list/view + session/memory_search + todo_list | 写文件 / process / git / http / mcp / 上列 |
| `web` | 是 | `models.web`，缺则 `subagent` | `web_search` `web_extract` `http_get` + 只读 fs | 写文件 / process / git / mcp / 上列；父无 web 工具 → 不建 Run |
| `vision` | 配了 `models.vision` | `vision` | `vision_analyze` + 只读 fs | 未配 role → 不建 Run |
| `speech` | 配了 `models.speech` | `subagent`（Whisper 不是 Complete；`models.speech` 只给 `audio_transcribe`） | `audio_transcribe` + 只读 fs | 未配 role → 不建 Run |
| `video` | 配了 `models.vision` | `vision` | `video_analyze` + 只读 fs | 未配 role → 不建 Run |

未知 kind / 未广告 kind / 空工具集 / 显式禁带请求：观察错误，**不建 Run**。`kind=web` 要求父集含 `web_search`/`web_extract`/`http_get` 之一。`vision`/`speech`/`video` RequireRole（未配 `models.*` 不建 Run）。

输出：子 Run 终态摘要（`task_id` ≡ `run_id` / kind / state / content / reused），写入父的 tool result。主模型与 TUI 只看见这张卡；子思考与工具迹留在 child `agent_run_records`，供续跑，不进主 packing。子 Prefix 要求短结论 + 路径/证据，不复述工具过程。

实现：kind 目录在 `internal/tools/taskkind.go`；`Agent → Tool Broker → Policy/Grant/Audit →` 编排 child Run → 同步 `agent.Run`。禁止 Broker 外直接调 Runner。不新开委派工具。

### 恢复

子 Run 各自 `agent_run_records`（ADR-030）。父在 `task` 未完成时崩溃：按 tool_calls + records 恢复规则处理，不得自动重放已成功的子副作用。

### 不做

- 异步并行子 Run、跨 session 子代理、新进程/模块框架、Hermes `delegate_task` / `role=orchestrator`。同 session 内显式 `task_id` 可跨用户轮次（kernel task）续跑，不自动匹配。
- 恢复交互 Planner 审批轨。
- Job/cron 直接生成子 Run（定时 Job 只提交顶层 chat task，见 ADR-042；子 Run 仍仅由模型 `task` 工具触发）。
- worktree isolation、`/agents` overlay。

## 后果

- 首版已落地：migration 015、`task` 工具、`runmeta`、预算/深度 fail-closed。
- Phase S/W/M：kind 目录 + 叶子禁带；广告 `general`/`explore`/`web`，配了 role 才广告 vision/speech/video。子 Prefix 按 kind 系统词 + AGENTS.md。
- 主 TUI 聊天气泡与 live stream 与主模型相同：只有父 `task` 卡；不展开子迹。完整父子树 UI 非目标。
- Run-scoped usage 上卷已落地（可观测 only）；Task 级 `…/usage` 仍含全部 runs。
- Inbox steer 按 `run_id` 认领，子循环不领取父 steer。
- `task_id` resume：进程内 inflight 判 busy；DB `running` 且无 inflight 可接管。不把终态 run 拉回 `running`。
