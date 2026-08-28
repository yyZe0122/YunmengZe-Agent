# ADR-043：Tool-call 交互 Permission（Crush 式）

- 状态：Accepted（已实现）
- 日期：2026-08-06
- 更新：2026-08-13（H4：pending 只读 suggested_decision；不自动 decide）
- 更新：2026-08-28（Phase W：`web_search` / `web_extract` 与 `http_get` 同闸，similar = host）

## 背景

Session chat（ADR-038）对 workspace 工具做 **preauth grant**；高风险工具（`process_exec` / `process_shell` / `git_*` / 交互 agent 的 `http_get` / `web_search` / `web_extract`）经 `chat.permission.allow` / `chat.tools.*` 记住 process/git，或 Tab **Auto** 本 session 预授 process+git（shell 与 exec **同一闸**）。`http_get` / `web_search` / `web_extract` / `vision_analyze`（url）**不**进 `permission.allow` / Auto 预授；交互 agent 广告后须 `/perm`（similar = 该 host）。未覆盖 grant 且 Policy 为 `require_approval` 时：交互 TUI Agent **挂起 `/perm`**；CLI / cron **立即 deny**。

Crush 等产品在 **单次 tool call** 边界提供 allow/deny 队列，无需整单 plan 审批。产品需要：在 **agent** 模式下，对未预授权的高风险 call 可挂起 → 人在 TUI 决策 → 发 **scoped grant** → **同一 agent 循环**继续。

**不得**恢复交互 Planner / `waiting_approval` 整单轨 / plan-step `POST /v1/runs`（ADR-038 已删）。

## 决策

### 与 Planner 的区分

| | 交互 Planner（禁止） | Tool-call permission（本 ADR） |
| --- | --- | --- |
| 对象 | 整份 plan / plan-step Start | **单次** tool call（Broker 边界） |
| 任务状态 | 整单审批态（已删） | task/run 保持 running；call 挂起 |
| 授权 | 批 plan 后批量 grant | decide 后 **scoped** grant 再 `AuthorizeAndConsume` |
| Gateway | 已删除的人批 / plan-step Start | `GET /v1/permissions` + `POST /v1/permissions/{id}/decide` |

### 配置：`chat.permission`

```json
"chat": {
  "permission": { "allow": ["process"] }
}
```

| 面 | 行为 |
| --- | --- |
| **TUI Agent** | 无 matching grant + `require_approval` → **pending `/perm`** |
| **TUI Auto** | 本 session 预授 process+git；切走后下一轮按新档 |
| **`permission.allow` / `tools.*`** | Agent 也不再问该类 |
| **CLI / cron** | 立刻 deny（`interactive` 省略；`scheduled_*` / actor `scheduler` 永不 wait） |
| **`permission.mode`** | 仍接受 `preauth`/`ask` 以便旧配置 load；**运行时忽略** |

- **plan 模式**：永不因 permission 扩大写/exec。只读看 `execution_mode`，不是 `permission_stance=plan`。
- **Job/cron**：`scheduled_*` task id 或 actor `scheduler` → **不 wait**，立即 deny（fail closed）。
- **`interactive`**：本机 TUI 能力声明，不是鉴权。省略 stance 不覆盖已有 session 的 `permission_stance`。

### 运行时流

```text
agent loop → Broker.Execute
  Policy deny → denied
  require_approval && 无 matching grant:
    非交互（CLI/cron/`scheduled_`）→ denied
    交互 TUI Agent → pending
      → tool_permission_requests + Waiter
      → TUI /perm 或 API decide
      → IssueGrant（plan 内 once/session scope）→ 同 call 再 Execute（禁嵌套 wait）
  deny → tool 结果 denied 回模型
```

### 持久化与 API（已实现）

- migration **018** `tool_permission_requests`
- `internal/toolpermission`：Store、Waiter、Service.Decide、Gate（Broker）；可选 `Events *events.Store`
- Gateway：
  - `GET /v1/permissions?session_id=&limit=`
  - `POST /v1/permissions/{id}/decide` body：`{ "decision": "allow_once"|"allow_similar"|"allow_permanent"|"deny", "actor": "…", "confirm": false }`
- TUI：`/perm` 列表；`/perm once|similar|permanent|deny <id-prefix>`；热键 1–4
- H4：List pending 可带只读 `suggested_decision` / `suggested_reason`（once/similar 或 deny 提示）；**不**自动 decide、**不**建议 permanent。路径：双方皆空才只比 tool+capability；一侧空不匹配；非空须 `filepath.Clean` 后相等或带分隔符的目录前缀（`/tmp/foo` 不匹配 `/tmp/foobar`）。有 `session_id` 时只查同会话。
- Agent（未预授）：chat plan **嵌入** process/git/`http_get`/`web_search`/`web_extract` 的 once + session CapabilityScope，**不**预发这些 grant（`issueChatGrants` 跳过）。`AllowedTools` **按名 unique**（ADR-052）：双 scope 仍给 `/perm` decide 选 once vs session，但模型只看见每个工具一次。host 类工具 similar 把请求 host 写入 grant。Plan / cron 不广告 `http_get` / `web_search` / `web_extract`。

### 事件 / SSE（C1）

CreatePending / Decide 成功后 **best-effort** 追加 Event Store 事件（不改变 decide 语义）：

| `event_type` | 时机 | 用途 |
| --- | --- | --- |
| `permission.pending` | Gate 写入 pending 行后 | TUI SSE → 触发 permission poll / 自动打开队列 |
| `permission.decided` | Decide allow/deny 后 | TUI SSE → 刷新 pending 列表 |

- `aggregate_type=tool_permission`；payload 含 `permission_id` / `session_id` / `tool` 等（无密钥）。
- **Decide 仍只经** `POST /v1/permissions/{id}/decide`（或 client `DecidePermission*`）。SSE `permission.decided` payload **可含** `decision`（无密钥），仅作 TUI 刷新信号，**不**替代 HTTP decide。
- 无 Events 配置时静默跳过 Append（单测 / 精简接线）。
- 轮询 list 仍保留作兜底（断线 / 旧客户端）。

### Grant 范围

- **allow_once**：单次 call；从 plan once scope 签发。
- **allow_similar**：本会话同 capability，路径尽量收窄到请求路径所属 plan 根；TTL ~24h。
- **allow_permanent**：需 `confirm:true` 二次确认；写 ConfigDir `permissions-trust.json`；长 TTL grant。
- **deny**：不发 grant。
- scheme A 路径/command 规则不变。见 ADR-046。

### 边界

- 副作用仍只经 Tool Broker；decide 只发 grant。
- Gateway 不调 provider、不跑 executor。
- Permission 推送复用通用 events SSE，不另开专用 permission 流。

## 后果

- 交互 TUI Agent 默认可 `/perm`；CLI/cron 仍 fail-closed。
- Tab Auto 是 session stance，不是 `permission.mode=auto`（该值仍拒绝）。
- Job 路径 fail-closed。
- TUI 可更及时弹出 pending 队列，而不依赖固定 2s poll  alone。

## 相关

- ADR-009 event schema；ADR-011 grants；ADR-012 Broker；ADR-038 session chat；ADR-018 Gateway；**ADR-046** session workspace + permission tiers（`allow_similar` / `allow_permanent`）；**ADR-052** 观察合同 / `http_get` ask。
