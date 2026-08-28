# ADR-041：Provider 视图上下文装配与窗压监控

- 状态：Accepted
- 日期：2026-07-31


## 背景

多轮 chat 与 tool 循环会把 transcript 与 tool 输出堆进 provider 请求。单条 tool/assistant **rune 裁剪**（`internal/agent/trim.go`）不够：需区分「任务累计 token 花费」与「当前窗填充」，并在长会话中做预算装配与压缩。

业界 coding agent（OpenCode / Cline / Claude / LangChain 等）普遍采用分层策略：单条瘦身 → 旧 tool 清空 → 保 tail 的摘要；持久全文与请求视图分离。

## 决策

### 双轨数据

| 轨 | 用途 | 真值来源 |
| --- | --- | --- |
| **Pre-flight 估计** | 装配 / 裁切预算 | 本地启发式（runes/4 + 消息 framing）× 按 model 的 EMA 校准 |
| **Post-flight 账本** | 累计花费、校准、窗压 | Provider `usage`（`Usage.PromptTokens()` = uncached + cache_read + cache_write） |

禁止用 `TaskUsage.TotalTokens`（寿命累计）充当 context ring。

### 装配管线（`internal/contextpack` + agent/chatsession）

1. **L1** 单条 body trim（与历史 trim 策略一致；DB 全量保留，ADR-030）。
2. **L2** 旧 tool 结果改 placeholder（保护最近 ~40k 估计 tool tokens；回收不足阈值则跳过）。
3. **L3** pair-safe 丢弃最旧完整 turn（保留至少最后一轮 user turn 与 leading system）。
4. **摘要（默认开）**：压力 ≥ 可用窗 75% 且确定性裁切后仍不够时，对 **full transcript head** 做 LLM 摘要（失败则 extractive）；写入 `session_compactions`；provider 视图 = summary + tail。
5. 可用窗：`contextWindow - maxOutput - reserve`。`contextWindow` 优先用户配置，否则 models.dev 目录，再否则 **1_048_576**。packing 输出项：用户 `maxTokens`/`limit.output`，否则 **128_000**（不再把 `>16384` 打回 8192）。
6. **Tool-pair 安全切分**：`SplitHeadTail` / `alignToolPairCut` 不拆开 assistant `tool_calls` 与其 tool 结果。

### 压缩触发点

| 触发 | 位置 | 持久化 |
| --- | --- | --- |
| **Turn start** | `chatsession` pin 后 `ContextView.Build` | 是 → `session_compactions` |
| **Provider overflow** | `agent` 单次 retry（`IsContextOverflow` → 同一 `Build`） | 否（仅 in-memory provider 视图） |
| **Mid-turn precheck** | tool 结果写入后、下一轮 `Stream` 前（增量 L1/L2，必要时 `Build`） | 否 |
| **手动 `/compact [focus]`** | `chatsession.ForceCompact` → `POST /v1/sessions/{id}/compact` | 是；可选 focus 注入摘要 prompt |

结构化摘要：`agent.CompactSummaryPrompt` + `CompactSummaryWithPrevious`（anchor merge 已有摘要）。

### Anti-thrash

短窗内对同一 session 的 **LLM** compact 有上限（默认 **3 次 / 10 分钟**，`session_compactions.created_at` 计数）。

- 超限：仍可 L1–L3 + **extractive** 摘要并写入 durable 行，**不**再调模型。
- 手动 `ForceCompact` **绕过** thrash 上限（用户显式意图）。
- 常量：`contextpack.DefaultAntiThrashMax` / `DefaultAntiThrashWindow`。

### 持久化（migration 016）

- `context_snapshots`：按 task 的最近窗压（last_prompt、usable、estimate、source、ratio…）。
- `session_compactions`：会话级摘要；**不删除** transcript / `agent_run_records`。

### Gateway API

- `GET /v1/tasks/{id}/usage` — 累计 spend（既有）。
- `GET /v1/tasks/{id}/context` — 窗压快照。
- `GET /v1/sessions/{id}/context` — 该 session 最近一条快照。
- `POST /v1/sessions/{id}/compact` — 强制 durable head 摘要；body 可选 `{"focus":"…"}`（chat 已配置时可用）。

响应中 `source` 标明 `provider_usage` / `local_estimate` / `none`（context 快照），或 compact 结果的 `llm` / `extractive`。不宣称与账单 token 逐位一致。费用仍以后台为准。

### 配置（`chat` 块）

```json
"chat": {
  "workspace": { "default": "client_cwd" },
  "allow_write": true,
  "compaction": { "enabled": true },
  "max_iterations": 0
}
```

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `compaction.enabled` | `true`（省略整块或字段时） | `false` 时仅 L1–L3，不调 LLM 摘要、不写入新 `session_compactions`；已有摘要仍可被加载；`ForceCompact` 也拒绝 |
| `max_iterations` | `0`（不硬顶） | 每 turn 的 agent step 上限；1–256 时最后一步 soft-landing；省略 / 0 = 只靠 Esc / 30min / token / loop-detect（ADR-052） |

`contextWindow` 仍在 **model** 配置上（非 `chat`）。省略时由 `internal/modelcatalog` 从 models.dev 填；未命中 → 1M。

### 边界

- Gateway 不跑 tokenizer、不调 provider 做 count；compact 由 chatsession + agent Compactor 执行，Gateway 只转发。
- 装配不绕过 Tool Broker / Grant；摘要调用不经 tools。
- 不接本地 token→$ 定价；不绑单一厂商 opaque server compact 作为主路径。
- Mid-turn / overflow compact 不替代 turn-start durable 摘要；下次 turn 仍以 `session_compactions` + `SessionTranscriptTail` 为准（ADR-051）。

## 后果

- 长会话由 token 预算驱动，而非仅消息条数。
- TUI 可展示 window pressure；CLI/客户端可读 context API 与 `/compact`。
- 摘要默认开；可用 `chat.compaction.enabled=false` 关闭额外 provider 调用。
- 循环上限由 `chat.max_iterations` 配置（默认不硬顶，ADR-052），与窗压独立。
- Anti-thrash 限制自动 LLM 摘要刷爆；手动 compact 仍可用。

Phase Q（ADR-051）把叠在一起的三条 pack 管线收成单 `ContextView.Build`：装配在 `executeChat` 且 pin 之后；`through_message_id` + 真 model 写入 `session_compactions`；CJK 启发式（CJK rune ≈ 1 token，其余 /4）；退役 `RunRequest.History`。TUI 分页仍走 ASC `SessionTranscript`，packing 用 `SessionTranscriptTail`。旧行 `through_message_id=''` 回退 keep-2-turns。Q-harden：热路径不再对整份 view 跑 L2/L3；through 滑出 Tail 窗时保留整段 tail。

## 相关

- 实现：`internal/contextpack`、`internal/agent/runner.go`、`internal/chatsession`、`internal/corequery`、`providerconfig.ChatConfig`、migration 016、TUI `/compact`。
- Provider 字段：`docs/wiki/provider-protocols.md`（`contextWindow`）。
- 交互 tool permission：ADR-043；记忆 `on_pre_compress`：ADR-044。
- 编码循环装配：ADR-051。循环语义（观察 / turn·step·inbox / `max_iterations`）：ADR-052。
