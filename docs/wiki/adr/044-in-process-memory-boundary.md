# ADR-044：In-process MemoryManager（Hermes 式分层）

- 状态：Accepted（产品化进行中）
- 日期：2026-08-06
- 更新：2026-09-03（`session_search` 查询排除 `parent_run_id` child runs）
- 更新：2026-08-13（H5-lite：default_ttl + 过期软归档）

## 背景

长会话压缩（ADR-041）会丢 head 细节；用户偏好与跨 turn / 跨会话事实需要有界记忆，但不能恢复独立 Memory 进程 / Module Runtime / 多 DB / 云端向量全家桶。

Hermes 的正确分工是：**策展记忆**（总进 system 的短事实）与 **历史检索**（transcript FTS，按需工具）分离——库可以厚，注入必须薄。

## 决策

### 形态（分层）

```text
TUI /memory · /memory archived · /refresh-memory
        │
        ▼
GET /v1/memory (只读) ── corequery
        │
chatsession ──► internal/memory.Manager (in-process)
        │            │
        │            ├─ L0 curated (user/global, high priority) ── 冻结注入 system
        │            ├─ L1 session facts ── 注入预算内
        │            ├─ L2 detail entries ── 仅 memory_search
        │            └─ L3 transcript FTS ── session_search 工具
        │
        └─ tools via Broker only (memory_* / session_search)
                    │
              core.db (单库；migration 019+020+022)
```

- **不是** 独立进程、多 `memory.db`、Gateway 业务写、`/v1/modules`。
- 注入有界（默认 ~2k runes）；**不**扩大 grant / policy。
- 无 external memory provider（Mem0/Honcho 等）。

### 层定义

| 层 | 存储 | 何时进 context |
| --- | --- | --- |
| **L0 curated** | `memory_entries`：`session_id=''` 且/或 `kind=curated` / 高 `priority` | **session 开始冻结**注入 system |
| **L1 session** | `session_id=当前`、`kind=session`（默认） | 冻结快照预算内 |
| **L2 detail** | `kind=detail` 或长条目 | **仅** `memory_search` |
| **L3 history** | `agent_run_records` → FTS 投影 | **仅** `session_search` |

### 注入策略（冻结）

- **默认**：每个 `session_id` 在 **首次 chat turn** 构建 system memory block 并缓存；同 session 后续 turn **不再** Prefetch 刷新（保 prefix cache、对齐 Hermes frozen snapshot）。
- **刷新**：TUI `/refresh-memory` 或内部 `Manager.InvalidateSnapshot(sessionID)` 后下一 turn 重建。
- 中途 `memory_write` 立即落库，但 **不**自动改已冻结 system 块。

### 注入扫描（H6-min）

- 包：`internal/injectscan`（确定性规则；无 LLM）。
- **写入**：`RememberKind` 在落库前 `Scan`；拒绝控制字符 / 不可见 Unicode·bidi / 常见注入标记 → `injectscan.ErrRejected`。
- **注入**：`formatBlock` 跳过脏条目；skill 正文 `Read`/`skillSystemMessage` 拒绝脏内容（skill 路径 skip inject + 日志）。
- 目标：fail-closed，不为远程/cron 文本开注入口；规则可后续收紧（见 optimization H6）。

### 生命周期钩子

| 钩子 | 调用点 | 作用 |
| --- | --- | --- |
| `Initialize` / `Shutdown` | daemon | 打开/关闭标记；启动时软归档过期条目 |
| `FrozenSystemBlock` | `executeChat` 首次/刷新后 | L0+L1 有界注入 |
| `SyncTurn` | turn 成功后 | remember/prefer 类 → L1（或配置） |
| `CurateTurn`（H1-lite） | turn 成功后 async | aux（`models.compact` 或 main）提案 → L1 `source=curator`；**不**改冻结块 |
| `OnPreCompress` | pack 摘要前 | head 短事实 → 默认 L2 detail |
| `Maintain`（H5-lite） | 启动 + 每 turn 成功后 | 过期条目标 `archived_at`；**不**硬删 |
| tools | Broker | search / write / promote / forget；session_search |

### LLM Curator（H1-lite）

- 主 run 完成后 `chatsession` 触发 `Manager.CurateTurn`（独立超时；失败仅日志）。
- Aux：`agent.Runner.ProposeMemoryFacts`（role `compact`，否则 main；无 tools）。
- 提案经 `injectscan` 后 `RememberKind`；默认 `kind=session`、`priority=5`。
- **不** `InvalidateSnapshot`；注入仍冻结至 `/refresh-memory` 或新 session。
- 配置：`chat.memory.curator`（`enabled` 默认 true；`max_facts` 默认 3；`timeout_ms` 默认 15000）。

### 存储（migration 019 + 020 + 022）

`memory_entries`：

| 列 | 说明 |
| --- | --- |
| `entry_id` | PK |
| `session_id` | 空 = user/global |
| `content` | 正文 |
| `source` | builtin / pre_compress / sync_turn / user / promote / curator |
| `tags_json` | 标签 |
| `kind` | `curated` \| `session` \| `detail`（默认 session；global 默认 curated） |
| `priority` | int，越大越优先注入（默认 0） |
| `expires_at` | 可空 RFC3339Nano；过期不注入、可软归档 |
| `archived_at` | 可空 RFC3339Nano；非空 = 已归档；默认 list/inject 排除 |
| `created_at` / `updated_at` | UTC RFC3339Nano |

- FTS5：`memory_entries_fts`（content 检索）。
- Transcript FTS：`transcript_fts`（由 `agent_run_records` 投影文本；**append 时 upsert**）。`AppendToolResult` 后写入 `transcript_search`（≤4000 runes，带 tool 名+path）；失败/取消 run 也索引已落盘行。`completeChat` 仍投影 user + 最终 assistant。不回填全部历史。

不删除 transcript / `agent_run_records` 行（append-only 不变）。

### 工具

| 工具 | 风险 | 模式 | 说明 |
| --- | --- | --- | --- |
| `memory_search` | R0 | agent+plan | L0–L2 FTS/LIKE；不注入 |
| `memory_write` | R1 | **仅 agent** | `action`: add\|replace\|remove；`global`/`kind`/`priority` |
| `memory_promote` | R1 | **仅 agent** | session entry → user/global curated |
| `session_search` | R0 | agent+plan | L3 transcript FTS；有界结果；排除 `runs.parent_run_id IS NOT NULL`（ADR-039） |

### 可见性（只读）

- `corequery.ListMemory` / `SearchMemory`
- `GET /v1/memory?session_id=&q=&kind=&limit=&offset=&include_global=&include_archived=`（`include_archived=true` 只返回归档行；`include_global` 默认 true）
- `POST /v1/memory/actions` `{action: refresh|forget|promote, session_id?, entry_id?}`（TUI `/refresh-memory` · `/memory forget|promote`）
- TUI `/memory`（list/search/forget 经只读 + 窄写服务或工具等价路径）；`/memory archived` 只看归档行
- TUI `/journey`：只读 `ListMemory` 前缀 + skill 事件轨（C4；见 ADR-050）
- **Gateway 不持业务写 `*sql.DB`**；forget/promote/refresh 写路径：工具或 daemon 内 `memory.Manager` 经专用 service（非 Gateway 内嵌 SQL 写）

### 配置 `chat.memory`

```json
"memory": {
  "enabled": true,
  "max_inject_runes": 2000,
  "inject_mode": "session_start",
  "default_ttl": "",
  "session_search": true,
  "curator": {
    "enabled": true,
    "max_facts": 3,
    "timeout_ms": 15000
  }
}
```

- `inject_mode`: 仅 `session_start`（冻结）；刷新靠 `/refresh-memory`。
- `default_ttl`: Go duration（如 `"168h"`）；空 = 不自动过期。仅套到 **session/detail** 且未显式 `expires_at` 的写入；**global curated 永不自动过期**；promote 到 curated 会清 TTL。
- 过期维护：启动 + 每成功 turn 将 `expires_at <= now` 标 `archived_at`。**不**自动 `DELETE`；硬删只走 `/memory forget` / `memory_write remove`。
- `curator.enabled=false` 可关 H1-lite 以省 token。
- 省略整块 → 启用 + 上表默认。

### 边界（不变）

- 单 `core.db`；Policy → Approval → Grant → containment → Audit。
- 无云端向量默认；无多 DB；Skill 不扩大授权。

## 后果

- 压缩后仍可保留偏好；跨会话靠 global curated + session_search。
- 细节可大量落库而不撑爆 system prompt。
- 架构保持三件套。

## 相关

- ADR-004 单库；ADR-041 压缩；ADR-038 chat；ADR-012 Broker；ADR-022 corequery；ADR-051 ContextView / L3 投影时机。
