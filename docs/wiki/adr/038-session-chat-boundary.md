# ADR-038：Session 多轮 Chat 双轨（OpenCode build/plan）

- 状态：Accepted
- 日期：2026-07-30
- 更新：2026-08-27（retract：`metadata.hidden_task_ids`；records 仍保留）
- 更新：2026-08-17（ADR-052：`AllowedTools` 按名 unique；ask 姿态 once+session scope 仍可双写 plan）

## 背景

早期 `agent` 多轮 chat 与 `plan`（Planner → 人批 → plan-step）两条管道与 OpenCode 的 **build / plan**（同一会话、权限不同）不一致。本 ADR 统一为双轨 chat。

## 决策

### 双轨 = 权限姿势，不是两条管道

| Tab | 内核 `execution_mode` | session `permission_stance` | 工具（Grant） |
| --- | --- | --- | --- |
| **plan** | `plan` | `plan` | workspace **只读** |
| **agent** | `agent` | `agent` | 读 + 写；未预授的 process/git **等 `/perm`** |
| **auto** | `agent` | `auto` | 读 + 写；**本 session 预授** process+git（切走结束） |

共性：

- 同一 Session / agent 循环 / transcript；
- 提交 `POST /v1/tasks` + `execution_mode`；
- synthetic plan + 系统审批记录 + Capability Grant（满足 Broker，非人类业务审批）；
- 副作用只经 Tool Broker（Policy → Grant → 路径/超时 → Audit）。

默认不预授权：`process_exec` / `process_shell` / `http_get` / `web_search` / `web_extract` / `git_*`。  
记住放行：`chat.permission.allow: ["process","git"]` 或 `chat.tools.*`（OR）。`process_shell` 与 `process_exec` **同一闸**。TUI Auto 只预授**当前 session**。**plan / cron 永不**获得这些能力。CLI/`ymz run` 无 `/perm`，高风险仍立刻 deny。

### 不得恢复（反回归）

- `internal/planner`、`planningrecovery`、`agentautostart`、`approvalsubmission`；
- tasksubmission 异步 PlanTask / 人批 StartRuns；
- Gateway 人批与 plan-step `POST /v1/runs`（已删除，非 410 兼容面）；
- TUI a/r、`/approve`、整单审批 overlay；
- 旧 plan-step Job 语义（由 ADR-042 chat-native Job 取代）。

### 配置

```json
"chat": {
  "workspace": { "default": "client_cwd", "allow": [], "allow_all": false },
  "allow_write": true,
  "tools": { "git": false, "process": false },
  "compaction": { "enabled": true },
  "max_iterations": 0,
  "permission": { "allow": [] }
}
```

- `workspace`（ADR-046）：会话根与天花板；
- `allow_write`：仅 agent 写权限天花板；**plan 永远只读**；
- `tools.git` / `tools.process`：与 `permission.allow` OR，仅 agent 预授权；
- Tab Auto：session `permission_stance`，不是第三种 `execution_mode`；
- `permission_stance=plan` **不**单独强制只读；只读只看 `execution_mode`；
- 省略 `permission_stance` 不覆盖已有 session 值；
- `permission.mode`：旧字段，load 接受 `preauth`/`ask`，**运行时忽略**。Wait = TUI `interactive`；CLI/cron fail-closed；
- 会话记忆：ADR-044；注入/skill 正文经 `injectscan`（H6）fail-closed；
- 会话模型偏好（O4）：`metadata.model` / `preferred_model`；不改全局 main；chat run 解析 **job pin → prefer → main**（见 ADR-045）；
- `chat.commands`（O3）：用户 slash 模板，仅 instruction；TUI 展开后作为 user 消息提交；不扩 grant。

### 任务控制

- `taskcontrol` 经 `ChatInterrupter` → `chatsession.Interrupt`；
- pause：task=paused；cancel：task=cancelled 并撤销 grants。

### 历史与上下文（ADR-041）

- transcript 在 `core.db` 全量保留（`agent_run_records` 不删）；
- `sessions.metadata.hidden_task_ids` 软藏已 retract 的 turn；packing、`SessionTranscript`、ListTasks、GetTask、runs、session 计数跳过这些 task；
- provider 视图经 `contextpack`；窗压经 Gateway context API 与 TUI。

### 不变

- Gateway 不执行 tool、不调 provider、不发 grant；
- 任务状态机：`created → running → (paused) → completed|failed|cancelled`；无 Planner 遗留态；
- Skill 与 `chat.commands` 仅指令文本，不扩大授权；TUI 可 `/skills`、`/skills apply|reject`、`/<skill-id>` 或 `/<command>` 显式使用（草稿见 ADR-050）。Prefix 注入技能目录（id+一句话）；正文仍 `skills_list` / `skill_view`（ADR-036 / 052）。
- 可选用户规则：`<ConfigDir>/AGENTS.md`（EnsureConfig 缺则种子）始终注入；`<workspace>/.yunmengze/AGENTS.md` 存在则追加。经 `injectscan`；不扩 grant。子代理继承同一套 overlay。
- Prefix：短身份（`YunmengZe Agent <version>` + 模式 + 已配 `models.*` 角色；不写死「No vision」；`/model` 只切 main）+ 共用工具协议。pin 之后另插一条 `<env>`（当前 model / workspace / UTC date）。用户/项目 `AGENTS.md` 仍是后面独立 system（preamble：不扩授权）。循环语义见 ADR-052。

## 结果

- Tab 语义对齐 OpenCode；单一 chat 编排器；
- `AllowedTools` 按名 unique；工具业务失败回灌模型（ADR-052）；运行中回车 = steer；`ask_user` 问题卡（perm 优先）；Prefix 含技能目录；交互 agent 广告 `http_get`/`web_search`/`web_extract` 不预发（plan/cron 不广告）；
- 定时 Job 见 ADR-042。
