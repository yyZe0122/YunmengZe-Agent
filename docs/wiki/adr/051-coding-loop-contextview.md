# ADR-051：编码循环 ContextView 与合同切片

- 状态：Accepted
- 日期：2026-08-14
- 更新：2026-08-27（QG：`edit_revisions.kind`；`fs_remove`；retract hide）

## 背景

长会话压缩标成「已关闭」是错觉。今日一条 chat turn 叠三条管线：

```text
StartChat.loadHistory
  SessionTranscript ASC LIMIT 500          ← 长会话看见最旧 500 条
  packSessionHistory                       ← pack #1：当时 main 窗、Apply("")、maxOut=8192（现行 packing 输出项见 ADR-041：unset → 128_000）
executeChat
  resolveRunModelPin                       ← 晚于 pack #1
  agent.Run({Messages, History})
    History 粘在 system 前面               ← L3 保的是摘要，不是 AGENTS/skills
    packForProvider                        ← pack #2：才用真 model
    overflow → Budget 8000 紧急策略        ← pack #3
```

半截改 History 前缀或只翻消息序，比不改更危险。Phase Q 把压缩侧收成 **一个 ContextView builder**，按 QB1→QB5 连续落地；文件 / shell / todo / L3 / 检查点 / TUI 是独立模块（QC–QH）。

产品切片已关闭（摘要在 [`docs/backlog/current.md`](../../backlog/current.md)）。本 ADR 是合同，不另开优化笔记。

## 决策

### ContextView

装配只发生一次预算、一个顺序。类型在 `internal/contextpack`：

```text
Prefix     稳定：sys（短身份 + 工具协议）+ `<env>`（pin 后：model / workspace / date）+ AGENTS + skills + frozen memory
Summary    少变：durable session_compactions
Tail       增长：packed 历史（不含当前 user）
Ephemeral  每轮变：todos 块 + 当前 user
Messages() = Prefix + Summary + Tail + Ephemeral
```

- `Build` 只对 **Tail** 做 L1–L3。Prefix **不得**进入 `dropOldestTurn`。
- extractive 摘要 **newest-first** 选取（预算紧时保住近轮路径/错误），写出时可再正序。
- 估计：CJK rune ≈ 1 token，其余 /4。校准器按 **真 model id** `Apply`；禁止 `Apply("")`。
- 可用窗：`UsableWindow(contextWindow, packingOutput, reserve)`。packing 输出项来自用户 `maxTokens`，未配置时 **128_000**。`plan.Budget.MaxTokens`（默认 128_000）是 run 寿命预算，不再在 `MaxOutputTokens==0` 时顶进请求 body。

### 装配时机

- 挪到 `executeChat`，且在 `resolveRunModelPin` **之后**（job pin → session prefer → main）。
- `StartChat` 不再同步 `loadHistory`。pack 失败走后台 `failChat`（start HTTP 已成功）。
- TUI/API `SessionTranscript` 仍 ASC 分页。packing 用新读模型 `SessionTranscriptTail`（从新往旧取，再正序交回）。禁止共用一个 query。

### 退役 `RunRequest.History`

- 删除 `History` 前缀粘贴。`Prepare` **只**持久化 Prefix + 当前 user。
- Provider 列表以 Prefix 的 system 开头：`View.Messages()`。
- 子 agent（`task` 工具）不继承父 Tail。
- 旧 compaction 行 `through_message_id=''` 回退 keep-2-turns；新行写 `through_message_id` + 真 model。

### 单 packer

overflow / mid-turn 调同一 `Build`。loop 内只对**新 tool body** 增量 L1/L2。禁止第三套 8k 紧急策略。`observeUsage` 在发生摘要或 L3 丢轮时设 `Compacted`。

### 模块边界（本 Phase 其余切片）

| 模块 | 边界 |
| --- | --- |
| **QE todo** | `session_todos`；`todo_list` / `todo_write` 经 Broker（R0，plan 可用）。清单进 **Ephemeral**，禁止进 Prefix。**不是** Planner / `tasks` 表。 |
| **QF L3** | `AppendToolResult` 后投影 `transcript_search`（≤4000 runes，带 tool 名+path）。失败/取消 run 也索引已落盘行。不回填历史。 |
| **QG checkpoint** | `edit_revisions.kind`：`modify` 写回、`create` 删除、`mkdir` 空目录 rmdir、`delete`（`fs_remove`）写回。失败 fail-closed。人径 `/undo` · `/edit`（hide）· `/editundo`（rewind 成功后再 hide）。retract 清该轮 L3 `transcript_search`、会话 todos、through 落在该轮上的 compaction。空文件 create/modify 不可分，undo 可能删文件。不给模型 rewind 工具。不撤 git / `process_shell` / 递归删目录。curated `memory_entries` 不动。 |
| **QC / QD / QH** | 文件工具原地加强；`process_shell` 与 `process_exec` 同一闸；TUI 只经 `gatewayclient`。 |

schema：`migrations/core/026_coding_loop.sql` 建 `session_todos` + `edit_revisions`；`028_edit_revision_kind.sql` 加 `kind`。实现：`internal/contextpack`、`internal/chatsession`、`internal/agent`、`internal/corequery`、`internal/sessiontodo`、`internal/editrev`、`internal/tools`。

## 不做

三件套、Broker 唯一副作用、plan 只读、cron fail-closed、禁止 yolo、TUI 不进 tools。不合并 `contextpack` / `chatsession` / `agent`。不改 `fs_*` 工具名。不做第二套 `apply_patch`、Levenshtein、git reset、给模型 rewind。

QB 内部禁止拆开发布：只合 QB2 或只改消息序、保留 `History`，半截比不改更危险。

## 后果

- 长会话 packing 看见最近 turns，不再被 ASC LIMIT 500 钉在最旧头。
- Prefix（AGENTS / skills / frozen memory）在 L3 下稳定，利于 prefix cache。
- pin 的窗与 `maxTokens` 参与预算；CJK 估计更接近真窗。
- 修订 ADR-041 / ADR-044 后果段（不另起平行文档）。
- QA–QH + Q-harden 已发 **v0.2.8**。人径 `/undo` · Esc Esc → `POST /v1/sessions/{id}/rewind`。`/edit` · `/editundo` → `POST /v1/sessions/{id}/retract`。
- **Q-harden：** 热路径 `packForProvider` 只做 L1，不再对整份 ContextView 跑 L2/L3。`through_message_id` 滑出 Tail 窗时保留整段 tail（禁止 keep-2 砍中间轮）。新 compaction 行写真 model id。mid-turn rebuild 把 todo 块留在 Ephemeral。`HistoryBudget` 不超过 usable。

## 相关

- 切片摘要：[`docs/backlog/current.md`](../../backlog/current.md) Phase Q（已关闭）
- ADR-041 装配/窗压；ADR-044 记忆与 L3；ADR-038 chat；ADR-022 corequery；ADR-045 模型 pin
- 循环语义（失败回灌 / turn·step·inbox）见 [ADR-052](052-coding-loop-harness.md)；本 ADR 只管装配。
- 实现：`internal/contextpack`、`internal/chatsession`、`internal/agent`、`internal/corequery`、`internal/sessiontodo`、`internal/editrev`
