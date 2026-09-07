# AGENTS.md

Local-first Go automation agent. Module: `github.com/yyZe0122/yunmengze-agent`. Go **1.26+**.

## Production shape

Only three production pieces:

- `ymzd` — long-running daemon (composition root: `cmd/ymzd`)
- `ymz` — local CLI client of the gateway (`cmd/ymz`); no-arg and `tui` open the TUI; TUI/`run` ensure a unique daemon via `internal/daemonctl` (`ymz start|stop|restart|status` or `ymz daemon …`)
- `core.db` — single SQLite source of truth (`modernc.org/sqlite`, pure Go; builds use `CGO_ENABLED=0`)

Do **not** restore deleted architecture: Module Runtime/Supervisor, out-of-process Memory/Scheduler/Evolution/Echo, `skills.db` / `scheduler.db`, `/v1/modules`, multi-DB, ORM, container DI, or generic event-bus frameworks.

## Commands

| Goal | Linux/macOS | Windows |
| --- | --- | --- |
| Format | `make format` | `.\scripts\dev.ps1 -Action format` |
| Check (fmt + vet + test [+ systemd unit]) | `make check` | `.\scripts\dev.ps1 -Action check` |
| Package VS Code VSIX (optional; Node) | `make vscode` | — |
| Build → `bin/` | `make build` | `.\scripts\dev.ps1 -Action build` |
| Install to PATH | `make install` → `~/.local/bin` | `.\scripts\dev.ps1 -Action install` |
| check + build + daemon `--check` | `make all` | `.\scripts\dev.ps1 -Action all` |
| Uninstall from BINDIR | `make uninstall` | `.\scripts\dev.ps1 -Action uninstall` |
| Clean `bin/` `dist/` + go cache | `make clean` | — |

```bash
go test ./... -count=1
go test ./internal/<pkg>/ -count=1
go test ./internal/<pkg>/ -run TestName -count=1
```

Dependency edits: `go mod tidy && go mod verify` and keep `go.mod`/`go.sum` clean (CI fails on drift).

Local release matrix: `goreleaser release --snapshot --clean --parallelism 1`.

**Publish (root only on this host):** batch-commit the dirty tree by feature (never one-shot a multi-feature dump), write **`docs/history/changelog/vX.Y.Z.md`** (this file **is** the GitHub Release body; empty stub fails), reset `unreleased.md`, then  
`sudo -i && cd /home/yyze/projects/AutoZeAgent && ./scripts/publish-release.sh vX.Y.Z --yes`  
(`--commit-paths changelog` only for a leftover notes commit.) Every tag **must** attach `ymz-vscode_{version}.vsix` (fail-closed; Node 18+). Full runbook: [`docs/release.md`](docs/release.md). Only `scripts/publish-release.sh` publishes — do not invent parallel scripts or steps.

`make check` on Linux also runs `scripts/check-systemd.sh` (no-ops on non-Linux / missing `systemd-analyze`).

## Layout that matters

| Path | Role |
| --- | --- |
| `cmd/ymzd` | Composition root: `main.go` (`run` + flags/defer) + `wire_{store,tools,chat,gateway}.go` + `adapters.go`. Same package; no DI container |
| `cmd/ymz` | Gateway client only — no tools, no provider, no grants; no-arg/`tui` → `internal/tui` |
| `internal/gateway` | Local HTTP/UDS server only (`api.go` + `handlers_*.go` + LocalRunner) |
| `internal/gatewayclient` | Shared CLI+TUI facade: HTTP/SSE transport + typed helpers (no import of gateway server) |
| `internal/tui` | Charm v2 TUI (ADR-053); Gateway-only; slash `cmds.go` + `cmds_*.go`; Elm `update.go` + `update_*.go`; **bubbles/v2** textarea + **lipgloss/v2** + **glamour/v2**; lipgloss 色块分区（无 Ultraviolet 整页 / lazy list）；header 1 行 + 底细线；status 在 editor 下；IME 硬件光标；slash 补全盖对话底；fold e/E/c；**no mouse grab** |
| `internal/kernel` | Session/Task/Run state machines (`model.go`) + repository (`repository.go` + `repository_{session,task}.go`) |
| `internal/tools` | Tool Broker (`broker.go`) + builtins; FS `fs.go` + `fs_{read,write,search,edit}.go`（含 `fs_remove`）；`process_exec` + `process_shell`; `taskkind.go` + `web.go` + `media.go`; nested `internal/executor` unimportable |
| `internal/architecture` | Import-boundary tests (ADR-022 / G4): pkg/gateway/TUI/CLI walls |
| `internal/chatsession` | Multi-turn chat for agent (build/write) and plan (read-only); workspace grants by mode |
| `internal/tasksubmission` · `taskcontrol` · `corequery` | Submit / pause·resume·cancel / read model (Gateway has no business `*sql.DB`) |
| `internal/providerruntime` | Main provider load, `/model`, hot-reload (ADR-048) |
| `internal/providerconfig` | ConfigDir JSON load + `chat.*` / MCP / role-map structs |
| `internal/modelcatalog` | Embedded models.dev snapshot + keyword lookup + ConfigDir cache refresh |
| `internal/configreload` | ConfigDir fsnotify debounce for provider files |
| `internal/agent` | Provider tool loop (ADR-052 R1–R5: observation; turn/step/next-step inbox) |
| `internal/contextpack` | Provider-view `ContextView.Build` + pack/compact; snapshots (ADR-041/051) |
| `internal/sessiontodo` · `internal/editrev` | Session todos (QE); file-edit checkpoints + rewind (QG) |
| `internal/memory` | In-process MemoryManager + `memory_entries` (ADR-044) |
| `internal/skillcatalog` · `skillmaintain` | File skills + draft/apply/usage (ADR-034/050) |
| `internal/toolpermission` | once/similar/permanent/deny gate (ADR-043) |
| `internal/userquestion` | `ask_user` pending + waiter + decide (ADR-052 R4) |
| `internal/modelresolve` | Job pin → session prefer → main (ADR-045) |
| `internal/injectscan` | Fail-closed scan before memory/skill system inject (H6-min) |
| `internal/opencodeimport` | Map OpenCode `opencode.json` → `agent.local.json` (CLI import) |
| `internal/scheduler` + `scheduledtasks` | In-process job store + chat-native fire → tasksubmission (ADR-017/042) |
| `internal/runlog` | Shared slog field helpers for the daemon log chain (ADR-047) |
| `internal/*` | Domain + app services (not a monorepo) |
| `pkg/{event,provider,scheduler,tool}api` | Stable cross-boundary contracts |
| `migrations/core/*.sql` | Embedded ordered migrations (`embed.go`); applied in `internal/store/sqlite.Open` |
| `docs/` | Catalog: `docs/README.md` — wiki / history / backlog |
| `docs/wiki/` | Design KB: ADR index `wiki/README.md`, `wiki/adr/`, `wiki/database.md` |
| `docs/history/changelog/` | Per-tag release notes (`vX.Y.Z.md` + `unreleased.md`) |
| `docs/backlog/current.md` | **Only** living optimization doc |
| `extensions/vscode` | VS Code Webview 聊天（ADR-056）+ TUI 启动器回退（ADR-054）；not in `go.mod`; `make check` does not run npm. User/install: `docs/wiki/vscode.md` |

### Wiring rules

- **Writes** go through narrow services: `tasksubmission` (submit)、`chatsession` (chat runs)、`taskcontrol` (pause/resume/cancel). **Reads** go through `corequery`. Gateway must not hold a business `*sql.DB`.
- Model-requested effects go **only** through Tool Broker (`internal/tools`, `RegisterBuiltins`). Nested `internal/tools/internal/executor` is intentionally unimportable outside `tools`.
- Scheduler is **in-process** on Core’s shared `*sql.DB` — no separate scheduler DB or process. Jobs are **chat-native** (ADR-042): due fire → `tasksubmission` → `chatsession`.
- Skills are file-based: `<config_dir>/skills` and `.yunmengze/skills` as `<id>/SKILL.md`. Instruction text only — never approvals, grants, or policy expansion. TUI `/skills` or `/<skill-id>` **explicitly preloads** bodies into the task snapshot. Otherwise the model loads via Hermes-style **`skills_list` → `skill_view`** (optional `file_path` for linked files; archived hidden). Agent may write `SKILL.md.draft` (`skill_draft`); apply/reject is human-only (`/skills apply|reject`). Optional `chat.commands` templates (O3): `/<cmd> [args]` expands `$ARGUMENTS` into a user message only. Slash priority: built-in → `chat.commands` → skill id.
- User rules: `<config_dir>/AGENTS.md` (seeded on EnsureConfig if missing) always injected; `<workspace>/.yunmengze/AGENTS.md` appended when present. `injectscan`; no grants. Not the repo-root contributor `AGENTS.md`.
- Sub-agents (if any) = logical child Runs with `parent_run_id` + `task` tool, not new processes/modules (ADR-039). `task.kind` catalog: advertised `general`/`explore`/`web`; `vision`/`speech`/`video` when the matching role is configured; children are always leaves (no nested `task`). Optional `task_id` (value = child `run_id`) resumes the same child; omit starts a new one. Session packing / TUI / `session_search` exclude child records; the parent sees only the `task` observation.
- CLI and TUI are **peers**: both use `gatewayclient`; TUI does **not** shell out to CLI subcommands. **TUI is the primary UX**; CLI is secondary (scripts/automation).

## Hard constraints

- Preserve Policy → Approval → Capability Grant → path containment → timeout/output limits → Audit on every tool path. Fail closed.
- Gateway does not execute tools, call providers, or issue grants.
- Migration filenames are lexicographically ordered and **immutable after release** (keep history even when later migrations drop tables).
- Persist/serve times as UTC RFC3339Nano; convert for display only at CLI/UI edges.
- Never commit secrets, `agent.local.json`, `*.db`, logs, sockets, or `bin/`/`dist/` output.
- Prefer concrete types, small interfaces at call sites, explicit errors, and `context.Context` on cancellable work. No hidden global orchestration.

## Config / runtime

- Modes: `--mode user|system`. User mode is flat **`~/.yunmengze`** on all OS (`YMZ_HOME` override); system mode keeps OS system paths (Linux `/etc/yunmengze`, …). Not XDG-split.
- Provider config is **ConfigDir only** (not project cwd): user mode is flat **`~/.yunmengze`** (all OS; override `YMZ_HOME`); system mode keeps OS system paths. Files: `agent.local.json` then `agent.json`. Daemon `EnsureConfig` seeds a template if ConfigDir has no file (does **not** copy from project/cwd). Prefer `{env:VAR}` / file refs over literal API keys.
- **Hot-reload (ADR-048):** while `ymzd` runs, edits to `agent.json` / `agent.local.json` / `env` rebuild the **main** provider stack (~0.5s debounce; `internal/providerruntime` + `configreload`). Fingerprint hashes API key/headers (no secrets in logs). Process non-empty env still wins over `env` file. **Not** reloaded: `chat.*`, MCP, role map. If daemon started without agent/chat, fix config then **`ymz restart`** (no late-bind).
- Per-model options: `maxTokens` = output cap (omit → do not send `max_tokens` except Anthropic); optional `contextWindow` = packing / UI window. Omit both to fill window from **models.dev** (`internal/modelcatalog`; miss → 1M / packing 128k). OpenCode `limit.{context,output}` alias accepted. See `docs/wiki/provider-protocols.md`, ADR-041.
- Provider selection is OpenCode-style: top-level `model` = `providerID/modelID…` (first `/` only; model segment may contain `/`). Catalog keys under `provider.<id>.models` match that segment; optional `models.<key>.id` is the wire/API id. Agent requests use the wire id, not a rewritten bare suffix.
- Optional **`models`** role map (ADR-045): `models.subagent` / `models.compact` as selection refs; omit → top-level `model` (main). `/model main` changes **global** main; role map changes need daemon restart. Unknown keys fail load. Optional `models.web` for `task(kind=web)` (omit → subagent). Optional `models.vision` / `models.speech` advertise auxiliary media tools (ADR-055). Chat Prefix = short identity (`internal/version.Version` + roles; extra configured roles listed; no hard-coded “No vision”) then a pin-after `<env>` (model / workspace / date). User `<ConfigDir>/AGENTS.md` is a separate overlay. Skill catalog (id + one-liner) is injected into Prefix; bodies still load via `skill_view`. Sub-agents inherit the same AGENTS.md overlay. TUI header and `/status` show the same version.
- Session **model preference** (O4): `sessions.metadata.model` via `PATCH /v1/sessions/{id}` `{preferred_model}`; TUI `/model [ref]` / Ctrl+L stores preference (no global switch). Ready-page pick is a **sticky** TUI draft (`/new` does not clear it; every new session from ready writes it as `preferred_model` until the user picks again; not persisted across TUI restart). `/model main provider/model` switches global main. TUI has no clear-prefer command (empty string only via `PATCH`). Chat runs resolve **job pin (H7) → prefer → main** (`internal/modelresolve`); invalid prefer falls back to main; invalid job pin fails start. Configured subagent/compact roles still win on those call sites.
- Optional **`chat`** in the same JSON: `workspace` (`default` client_cwd\|daemon_cwd\|abs path; `allow[]`; `allow_all`; ADR-046), `allow_write` (optional ceiling for **agent** writes; omit = true), `tools.git` / `tools.process` (aliases) and `permission.allow` `[process|git]` remember high-risk families (OR; **plan / cron never** get process grants), `compaction.enabled` (default true), `max_iterations` (omit/0 = no hard cap; 1–256 soft-lands; ADR-052), `permission.mode` (load-only `preauth`\|`ask`, ignored at runtime; Auto is session stance; ADR-043), `memory` (enabled/max_inject_runes/session_search/`default_ttl`; optional `curator` H1-lite; ADR-044), `skills.unused_ttl` (H5-skill soft-archive; empty=off), `commands` (O3 slash templates: map id → `{description, template}` with `$ARGUMENTS`; injectscan at load; no grants). Session workspace = client launch cwd on submit. Plan mode is always read-only. Chat packing is a single **`ContextView.Build`** after model pin (ADR-051): Prefix + Summary + Tail + Ephemeral (todos in Ephemeral, never Prefix). Manual compact: TUI `/compact [focus]`. TUI Tab cycles agent→plan→auto (kernel `execution_mode` stays agent|plan; Auto = session `permission_stance`, this-session process+git pre-grant). Interactive Agent waits `/perm` once|similar|permanent|deny on a permission card (SSE `permission.*`; H4 may hint; similar may prefix-match `go test *`). Extra-root `fs_*`/process/git absolute paths use the same four tiers (once = this call; similar = session `extra_roots`; permanent writes `agent.local.json` `chat.workspace.allow` and raises the process PathGuard). `ask_user` waits on a TUI question card (multi-question ←→, multi-select, Type your own answer; Esc Esc dismisses; SSE `question.*`; CLI/cron unavailable). CLI/`ymz run` fail-closed. Layered memory: TUI `/memory` `/memory archived` `/refresh-memory` `/journey` `/journey skills` (curator writes do not auto-refresh frozen inject; expired entries are soft-archived). Skills: `/skills` `/skills apply|reject <id>` `/skills archived`. File loop: `fs_read` offset/limit+sha256 → `fs_patch`/`fs_write` (optional `expected_sha256`, unified diff); `fs_remove` deletes one regular file (rewind restores it); `fs_glob` supports `**`. Session todos: `todo_list`/`todo_write` (R0, plan ok). Human rewind: TUI `/undo` · Esc Esc → `POST /v1/sessions/{id}/rewind`（最近一次写文件）。`/edit` → `POST /v1/sessions/{id}/retract` 隐藏上一轮并填编辑器；`/editundo` 同时撤回该轮全部文件。retract 会清该轮 `transcript_search`、会话 `session_todos`、以及 through 落在该轮上的 compaction（`agent_run_records` 仍保留）。会话循环：Shift+PgUp / Shift+PgDn。 (no model rewind tool). Running Enter steers the next step (`POST /v1/sessions/{id}/steer`). `/new` leaves to ready and cancels a running turn. Foldable timeline: `/expand` · `e`/`E`/`c`; drag-select copy; running Esc cancels the turn. Inject path uses `injectscan` (fail-closed). See ADR-038, ADR-041, ADR-043, ADR-044, ADR-046, ADR-050, ADR-051, ADR-052.
- Optional **`chat.web`** (Phase W): `search` `ddg` (default) \| `searxng` \| `tavily`; searxng needs `searxng_url`, tavily needs `tavily_key`. Interactive agent advertises `web_search`/`web_extract` (`/perm`, similar = host); plan/cron never. Changing `chat.*` needs `ymz restart`. See `docs/wiki/provider-protocols.md`.
- `ymz config validate` checks path layout + provider load/resolve + chat structure; never prints secrets.
- `ymz config import-opencode [path]` maps OpenCode config → `agent.local.json` (model/provider, allowed top-level `models.*` roles, stdio+remote MCP, `command`→`chat.commands`, compaction; drops plugins/LSP/oauth/`models.main`/unknown roles with warnings). Default path: `~/.config/opencode/opencode.json`.
- Daemon owns `core.db` lifecycle; components must not close the shared connection.
- Dual-track tasks (OpenCode-style): both modes → `chatsession`; **agent** = write grants, **plan** = read-only. No interactive Planner/approval path. TUI Tab cycles agent|plan|auto (Auto is session stance, not a third execution_mode).
- Scheduled jobs: fixed interval; default `execution_mode=agent`; **H7** pins `model_ref` at create (empty → current main); fire with empty/unresolvable pin → skip+fail ACK. Create via TUI `/cron [every objective]` or CLI `job create [--model]` (secondary). See ADR-042.
- Daemon structured logs: JSONL (`ymzd.jsonl`); stage fields `component` / `operation` / `result` + `session_id` / `task_id` / `run_id` / `trace_id` (ADR-047). Filter: `ymz logs --run|--session|--task|--component|--level|--tail`. Level: `YMZ_LOG_LEVEL`. Other CLI: `ymz health` · `paths` · `version` · `db check` · `run [--execution-mode]` · `task status|pause|resume|cancel` · `job create|list|status|pause|resume|cancel`. Integration debug = real machine + logs; keep package tests for safety/architecture (no full e2e harness).

## Deep dives

Index: `docs/README.md` → `docs/wiki/README.md`. Start with: `001-core-boundaries`, `004-database-ownership`, `012-tool-broker-execution-boundary`, `018-local-gateway-boundary`, `037-cli-daemon-lifecycle`, `038-session-chat-boundary`, `039-logical-child-runs`, `040-mcp-tool-broker`, `041-context-packing-and-pressure`, `042-chat-native-jobs`, `043-tool-call-permission-interaction`, `044-in-process-memory-boundary`, `045-model-roles`, `046-session-workspace-and-permission-tiers`, `047-structured-logging-and-debug-chain`, `048-provider-config-hot-reload`, `022-application-query-boundaries`, `050-in-process-self-improvement`, `051-coding-loop-contextview`, `052-coding-loop-harness`, `053-charm-v2-tui`, `054-vscode-terminal-launcher`, `055-auxiliary-media-boundary`, `056-vscode-webview-chat`. Schema map: `docs/wiki/database.md`. Status / backlog: `docs/backlog/current.md`. VS Code VSIX: `docs/wiki/vscode.md` (ADR-056). Model window catalog: `internal/modelcatalog`. PR norms: `CONTRIBUTING.md`.
