# Wiki — design knowledge base

Numbered ADRs plus the `core.db` map. Catalog: [`docs/README.md`](../README.md).

Living status / backlog (only): [`backlog/current.md`](../backlog/current.md). **当前线 v0.7.0**（VS Code V4 Webview + `/perm` grant 夹窗）。O5–O6 / H2 / 飞书 M* / Marketplace 等用户再提。  
Agent/contributor entry: [`AGENTS.md`](../../AGENTS.md).  
Database map: [`database.md`](database.md).

## Production shape

```text
ymzd   long-running daemon (composition root)
ymz    local CLI + TUI (Gateway clients only)
core.db        single SQLite source of truth
```

Do **not** restore deleted pieces: Module Runtime/Supervisor, out-of-process Memory/Scheduler/Evolution, multi-DB, `/v1/modules`, ORM, container DI, interactive Planner / plan-step Start.

## Start here

| Order | ADR | Topic |
| --- | --- | --- |
| 1 | [001](adr/001-core-boundaries.md) | Core process and package boundaries |
| 2 | [004](adr/004-database-ownership.md) | `core.db` ownership and migrations |
| 3 | [012](adr/012-tool-broker-execution-boundary.md) | Tool Broker is the only effect path |
| 4 | [018](adr/018-local-gateway-boundary.md) | Gateway + CLI/TUI client boundary |
| 5 | [037](adr/037-cli-daemon-lifecycle.md) | Daemon ensure / stop semantics |
| 6 | [038](adr/038-session-chat-boundary.md) | OpenCode-style agent build / plan RO chat |
| 7 | [022](adr/022-application-query-boundaries.md) | Writes via services, reads via corequery |
| 8 | [039](adr/039-logical-child-runs.md) | Logical child Runs (`task` tool, `parent_run_id`, kind catalog, explicit `task_id` resume) |
| 9 | [040](adr/040-mcp-tool-broker.md) | MCP stdio + remote HTTP/SSE via Tool Broker |
| 10 | [041](adr/041-context-packing-and-pressure.md) | Provider-view packing, compaction, context API |
| 11 | [042](adr/042-chat-native-jobs.md) | Chat-native Job/cron (timed chatsession submit; H7 model pin) |
| 12 | [043](adr/043-tool-call-permission-interaction.md) | Crush-style tool-call permission (≠ Planner) |
| 13 | [044](adr/044-in-process-memory-boundary.md) | In-process MemoryManager (Hermes lifecycle) |
| 14 | [045](adr/045-model-roles.md) | Optional model roles (main / subagent / compact); O4 session prefer + run resolve |
| 15 | [046](adr/046-session-workspace-and-permission-tiers.md) | Session workspace + permission tiers |
| 16 | [047](adr/047-structured-logging-and-debug-chain.md) | Structured logs + real-machine debug chain |
| 17 | [048](adr/048-provider-config-hot-reload.md) | Provider config watch + main-stack hot-reload; models.dev catalog fill |
| 18 | [050](adr/050-in-process-self-improvement.md) | In-process skill draft / habit hint / skill usage (H3/H4/H5-skill) |
| 19 | [051](adr/051-coding-loop-contextview.md) | Phase Q ContextView + coding-loop contract (QB–QH) |
| 20 | [052](adr/052-coding-loop-harness.md) | Phase R turn/step/next-step inbox + observation contract |
| 21 | [053](adr/053-charm-v2-tui.md) | Charm v2 TUI: 清宣纸 chrome, textarea, no mouse |
| 22 | [054](adr/054-vscode-terminal-launcher.md) | VS Code extension: terminal launcher for existing TUI; `useTerminal` 回退 |
| 23 | [055](adr/055-auxiliary-media-boundary.md) | Auxiliary vision/speech/video tools; main loop stays text |
| 24 | [056](adr/056-vscode-webview-chat.md) | VS Code Webview 第四 peer：活动栏列表 + 编辑器 Tab → `/v1/*` |

Also: O3 `chat.commands` (ADR-038 / [provider-protocols](provider-protocols.md)); O4/H7 run resolve (`internal/modelresolve`, ADR-045：job pin → prefer → main).

Also useful: [003](adr/003-policy-invariants.md) policy, [011](adr/011-approval-capability-binding.md) grants (domain), [013](adr/013-provider-planner-boundary.md) provider boundary (**interactive Planner superseded**), [017](adr/017-scheduler-module-boundary.md) in-process scheduler (+ [042](adr/042-chat-native-jobs.md) product semantics), [034](adr/034-file-based-skills-boundary.md) skills, [035](adr/035-standard-protocol-tool-boundary.md) protocol tools.

Provider / `chat.*` / OpenCode import: [`provider-protocols.md`](provider-protocols.md) (user table: [README · Configure](../../README.md#configure)). VS Code VSIX: [`vscode.md`](vscode.md).

## Index (by number)

| ADR | Title | Notes |
| --- | --- | --- |
| 001 | Core boundaries | Production three-piece shape |
| 003 | Policy invariants | Fail closed; grants bind plan hash |
| 004 | Database ownership | Single `core.db` |
| 006 | Linux runtime | Flat `~/.yunmengze` user root / OS system paths |
| 007 | Cross-platform abstraction | `internal/platform` |
| 008 | Threat model | Current host/agent threats |
| 009 | Event schema evolution | |
| 010 | Kernel state consistency | Optimistic concurrency |
| 011 | Approval / capability binding | |
| 012 | Tool Broker execution boundary | Nested executor unimportable |
| 013 | Provider / planner boundary | **Superseded** interactive path → 038; provider rules remain |
| 016 | Skills module boundary | **Deprecated** → 034 |
| 017 | Scheduler boundary | In-process on `core.db`; product = 042 |
| 018 | Local gateway boundary | CLI ‖ TUI via gatewayclient; TUI primary; slash + events SSE |
| 022 | Application query boundaries | Task submit + corequery (parts historical) |
| 023 | Approval decision application boundary | **Superseded** interactive path → 038 |
| 024 | Initial planning recovery | **Superseded** → 038 |
| 026 | Application error classification | |
| 027 | Query pagination / sorting / filtering | |
| 028 | Core identity types | |
| 029 | UTC time storage | |
| 030 | Agent run record recovery | |
| 031 | Unified provider streaming event | |
| 032 | Approval interaction semantics | **Superseded** interactive path → 038 |
| 033 | Target platform behavior | |
| 034 | File-based skills | Replaces 016 |
| 035 | Standard protocol tool boundary | |
| 036 | Task skill snapshot | Explicit `skill_ids` preload; model `skills_list`/`skill_view`; chatsession injects explicit snapshot |
| 037 | CLI / daemon lifecycle | |
| 038 | Session chat boundary | Dual-track agent/plan; Tab Auto stance; `permission.allow`; `AGENTS.md` inject |
| 039 | Logical child runs | `parent_run_id` + `task` tool (sync); kind catalog; child is always a leaf; explicit `task_id` resume |
| 040 | MCP via Tool Broker | stdio + remote Streamable HTTP / legacy SSE; no Module Runtime |
| 041 | Context packing / pressure | Token budget pack, compaction triggers, anti-thrash, `/compact` |
| 042 | Chat-native jobs | Timed session chat submit; lease from 017; TUI `/cron` primary |
| 043 | Tool-call permission interaction | TUI Agent `/perm` cards; extra-root once/session/permanent; Tab Auto session stance; SSE `permission.*` |
| 044 | In-process memory boundary | Layered L0–L3 memory; freeze inject; FTS; `/memory` `/memory archived` `/journey`; injectscan (H6); H1-lite curator; H5-lite `default_ttl` + soft-archive; no Module Runtime |
| 045 | Model roles | Optional `models.subagent` / `compact` / `web` / `vision` / `speech`; Prefix lists extra configured roles; O4 session prefer + H7 job pin resolve |
| 046 | Session workspace + permission tiers | Client cwd session root; extra-root `/perm` (once call / similar session / permanent config) |
| 047 | Structured logging / debug chain | slog JSON stage boundaries; `ymz logs`; tests vs real-machine |
| 048 | Provider config hot-reload | Main stack only; no late-bind chat; models.dev window fill |
| 050 | In-process self-improvement | H3 skill draft+apply; H4 habit hint; H5-skill last-used/archive; ≠ Evolution |
| 051 | Coding-loop ContextView | Single `Build`; retire `History`; todo / L3 / checkpoint boundaries |
| 052 | Coding-loop harness | Observation contract; turn/step/next-step inbox; steer / ask_user cards（R1–R5 已落地） |
| 053 | Charm v2 TUI | bubbletea/lipgloss/glamour v2 + 清宣纸 chrome；提问/授权卡；无 UV 整页 / lazy list；no mouse grab |
| 054 | VS Code terminal launcher | `extensions/vscode`; TUI 回退；Webview 见 056 |
| 055 | Auxiliary media boundary | Tool-internal Complete / Whisper; no multimodal main loop |
| 056 | VS Code Webview chat | 第四 peer；列表 + 编辑器 Tab；拖文件 `@path`；`/perm` grant 配套见 approval/toolpermission |

Missing numbers (002, 005, 014–015, 019–021, 025, …) are **historical gaps**, not missing files to recreate.

Files live under [`adr/`](adr/) as `NNN-kebab-title.md`.

## Conventions

- Filenames: `NNN-kebab-title.md`, lexicographic order.
- Significant architecture changes get a new ADR (see `CONTRIBUTING.md`); update this index.
- Do not invent parallel optimization notes; update `docs/backlog/current.md` only.
