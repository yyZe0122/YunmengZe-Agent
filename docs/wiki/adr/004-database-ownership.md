# ADR-004：数据库所有权

活人表图：[`docs/wiki/database.md`](../database.md)。

- 状态：Accepted
- 日期：2026-07-13
- 更新：2026-08-27（`edit_revisions.kind` migration 028，ADR-051 QG）
- 更新：2026-08-17（`user_questions` migration 027，ADR-052 R4）
- 更新：2026-08-10（memory productization migration 020 FTS / kind；ADR-044）
- 更新：2026-08-06（memory_entries ADR-044；tool_permission_requests ADR-043）
- 更新：2026-07-31（context_snapshots / session_compactions，ADR-041）
- 更新：2026-08-03（run parent / job chat payload，ADR-039/042）

## 决策

`ymzd` 独占一个 SQLite 数据库 `core.db`，它是 Session、Task、Plan、Approval、Grant、Run（含 `parent_run_id`，migration 015）、Agent 记录、Tool Call、Event、Audit、Skill 快照、Scheduler Job/Run/Lease（含 `execution_mode` / `skill_ids`，migration 017，ADR-042）、**context 窗压 / session 摘要**（migration 016，ADR-041）、**tool permission 队列**（migration 018，ADR-043）、**user_questions**（migration 027，ADR-052）、**edit_revisions.kind**（migration 028，ADR-051）、以及 **in-process memory 条目**（migration 019–020，ADR-044：分层 kind/priority/expires + FTS + transcript_search 投影）的事实源。摘要与记忆只影响 provider 请求视图，不删除 transcript 或 `agent_run_records`。retract 把 task id 写入 `sessions.metadata.hidden_task_ids`，不删行。

Scheduler 通过 Core 已打开的 `*sql.DB` 工作，不创建 `scheduler.db`；到期 Job 由 in-process `scheduledtasks` 提交 chat task（ADR-042）。旧的 `memory.db`、`evolution.db` 和其他模块数据库不再属于生产架构，也不会被 daemon 自动读取或迁移。会话记忆使用 `core.db.memory_entries`（in-process），不是独立 Memory 库。

Core migration runner 统一拥有 schema 版本。Migration 013 删除旧 Module Registry、消费位点和 Evolution Activation 遗留表；015–028 为后续追加。历史 migration 序号保留，避免破坏已经存在的 `core.db` 升级路径。

大对象仍进入 Artifact Store；SQLite 保存元数据、路径和内容哈希。Provider 配置及 Secret 引用保存在配置文件或环境中，不把明文密钥写入 `core.db`。

## 后果

- daemon 是唯一数据库生命周期所有者；子组件不得关闭共享连接；
- 同一业务动作尽量在一个 SQLite 事务中提交；
- 不再需要跨模块数据库事务、消费位点或最终一致性补偿框架；
- 备份和恢复以 `core.db`、Artifact 目录和必要配置为边界。
