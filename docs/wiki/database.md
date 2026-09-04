# core.db

`ymzd` owns one SQLite file. Column-level truth is `migrations/core/*.sql` (lexicographic, immutable after release). Ownership: [ADR-004](adr/004-database-ownership.md). Times: UTC RFC3339Nano ([ADR-029](adr/029-utc-time-storage-and-presentation.md)). Secrets stay in config/env, not in the DB.

Catalog: [`docs/README.md`](../README.md). ADR index: [`README.md`](README.md).

## Shape

```text
sessions → tasks → runs → tool_calls
                ↘ plans → plan_steps → approvals → capability_grants
                ↘ task_skill_snapshots
jobs → job_runs / job_leases
memory_entries (+ FTS) · transcript_search (+ FTS)
tool_permission_requests · user_questions · context_snapshots · session_compactions
skill_usage · skill_events
session_todos · edit_revisions
events (append-only) · audit_log · artifacts · agent_run_records
```

Daemon is the only lifecycle owner. Components must not close the shared `*sql.DB`. Scheduler and memory are in-process on this connection — no `scheduler.db` / `memory.db`.

## Live tables

| Table | Role | ADR | Since |
| --- | --- | --- | --- |
| `sessions` | Session + `metadata`（O4 prefer；`hidden_task_ids` retract 软藏；`extra_roots` 会话 extra-root `/perm` similar） | [038](adr/038-session-chat-boundary.md) / [045](adr/045-model-roles.md) / [046](adr/046-session-workspace-and-permission-tiers.md) / [051](adr/051-coding-loop-contextview.md) | 001 |
| `tasks` | Dual-track `execution_mode` | [038](adr/038-session-chat-boundary.md) | 001 + 014 |
| `plans` / `plan_steps` | Plan hash + steps (grant chain) | [011](adr/011-approval-capability-binding.md) | 001 + 006 |
| `approvals` / `capability_grants` | Approval → scoped grant | [011](adr/011-approval-capability-binding.md) | 001 |
| `runs` | Run SM; `parent_run_id` children; `child_kind`/`child_tools` for `task_id` resume | [039](adr/039-logical-child-runs.md) | 001 + 011 + 015 + 029 |
| `tool_calls` | Broker call record | [012](adr/012-tool-broker-execution-boundary.md) | 001 / 005 |
| `task_skill_snapshots` | Explicit preload; immutable | [036](adr/036-task-skill-snapshot.md) | 010 |
| `jobs` / `job_runs` / `job_leases` | Chat-native cron; H7 `model_ref` | [042](adr/042-chat-native-jobs.md) | 012 + 017 + 021 + 024 |
| `tool_permission_requests` | once / similar / permanent / deny | [043](adr/043-tool-call-permission-interaction.md) | 018 + 025 |
| `user_questions` | `ask_user` pending / answered / unavailable / cancelled | [052](adr/052-coding-loop-harness.md) | 027 |
| `context_snapshots` / `session_compactions` | Window pressure; no transcript delete；`through_message_id` + model 由 ADR-051 写满 | [041](adr/041-context-packing-and-pressure.md) / [051](adr/051-coding-loop-contextview.md) | 016 |
| `session_todos` | 会话 Todo（非 `tasks`） | [051](adr/051-coding-loop-contextview.md) | 026 |
| `edit_revisions` | 本 agent 写/删文件检查点；`kind` create\|modify\|mkdir\|delete | [051](adr/051-coding-loop-contextview.md) | 026 + 028 |
| `memory_entries` + `memory_entries_fts` | L0–L3 facts; `archived_at` soft-archive | [044](adr/044-in-process-memory-boundary.md) | 019–022 |
| `transcript_search` + `transcript_fts` | L3 projection; source is run records | [044](adr/044-in-process-memory-boundary.md) | 020 |
| `skill_usage` / `skill_events` | last-used / draft·apply·archive | [050](adr/050-in-process-self-improvement.md) | 023 |
| `agent_run_records` | Recovery + transcript source | [030](adr/030-agent-run-record-recovery.md) | 009 |
| `events` | Append-only; UPDATE/DELETE abort | [009](adr/009-event-schema-evolution.md) | 001 |
| `audit_log` | Security / isolation audit | [008](adr/008-threat-model.md) | 001 |
| `artifacts` | Blob metadata + path + hash | [004](adr/004-database-ownership.md) | 001 |

FTS virtual tables are not independently owned — they project `memory_entries` / `transcript_search`.

## Removed (do not restore)

Dropped in migration 013: `module_registry`, `module_offsets`, `evolution_activation_*`. Also gone: separate `skills.db` / `scheduler.db` / `memory.db`.

## Rules

- New table or column → new lexicographic migration; never rewrite a released file.
- Significant schema change → ADR first, then SQL, then this page.
- Backup boundary: `core.db` + artifact dir + ConfigDir (no secrets in the DB dump story).
