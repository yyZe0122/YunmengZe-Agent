# AGENTS.md

## 协作约定

- 除非用户明确要求，不主动运行测试，也不额外编写测试代码。
- 只修改任务要求的范围；不懂或拿不准时询问用户。
- 优先最简设计，不做过度抽象；内部调用不堆叠无意义判空，外部输入与服务响应保留必要校验。
- 可以提出异议，但未经同意不擅自改变用户决定。

Local-first Go coding agent. Module: `github.com/yyZe0122/yunmengze-agent`. Go **1.26+**, builds use `CGO_ENABLED=0`.

## Production shape

- **`ymz`** is the only executable. No arguments or `tui` opens the terminal UI; `run` is for scripts.
- CLI and TUI call **`internal/agent.Engine` directly**, in the same process.
- **`core.db`** is the single SQLite source of truth (`modernc.org/sqlite`). It stores sessions, messages, compaction summaries and memory. The engine owns its connection.
- Exactly two execution modes: **plan** is read-only; **agent** executes directly with the current OS account's access. There is no approval, grant or Auto stance.

Do not introduce a daemon, HTTP Gateway, module runtime, scheduler, permission service, event bus, DI container, ORM, multi-DB architecture or VS Code extensions. The current design is documented in [docs/wiki/architecture.md](docs/wiki/architecture.md).

## Workflows

| Goal | Linux/macOS | Windows |
| --- | --- | --- |
| Format | `make format` | `./scripts/dev.ps1 -Action format` |
| Format check + vet (no tests) | `make lint` | `./scripts/dev.ps1 -Action lint` |
| Format + vet + tests | `make check` | `./scripts/dev.ps1 -Action check` |
| Build `bin/ymz` | `make build` | `./scripts/dev.ps1 -Action build` |
| Check + build | `make all` | `./scripts/dev.ps1 -Action all` |
| Install | `make install` | `./scripts/dev.ps1 -Action install` |
| Uninstall executable | `make uninstall` | `./scripts/dev.ps1 -Action uninstall` |

The commands above are manual references, not permission to run tests automatically. When requested, use `go test ./... -count=1` or target the changed packages. Keep `go.mod` and `go.sum` in sync after dependency edits. Release snapshot: `goreleaser release --snapshot --clean --parallelism 1`.

Only `scripts/publish-release.sh` contains publishing logic; `scripts/publish-release.ps1` is its Windows wrapper. Local runs validate and atomically push main plus a tag; the Release workflow calls the same script to build/upload. Follow [docs/release.md](docs/release.md). Batch commits by feature. Each tag's nonempty `docs/history/changelog/vX.Y.Z.md` is the release body; reset `unreleased.md`. Release assets contain only `ymz` for each supported platform, plus checksums; no VSIX or Node dependency.

## Code boundaries

| Path | Responsibility |
| --- | --- |
| `cmd/ymz` | CLI flags, OS signals, configuration directory and engine lifetime |
| `internal/tui` | Charm v2 UI; direct engine calls, no tool execution or database connection |
| `internal/agent` | Explicit composition, provider loop, child sessions, optional features and compaction |
| `internal/session` | Conversation persistence and per-session lease; mode is `agent` or `plan` |
| `internal/tools` | Concrete tool registry; file, shell, web and image implementations |
| `internal/providers` + `pkg/providerapi` | Protocol adapters and provider-neutral streaming messages |
| `internal/providerconfig` | ConfigDir-only JSON/env/rules; strict unknown-field validation |
| `internal/contextpack` | Token estimates, context packing and complete tool-call pairs |
| `internal/skillcatalog` | Discover/read `SKILL.md` and linked files; no maintenance workflow |
| `internal/memory` | Shared-DB user preferences, per-run injection, evidence, TTL and low-frequency curation |
| `internal/mcp` | Stdio and HTTP/SSE clients; connected only when invoked in agent mode |
| `internal/modelcatalog` | Embedded models.dev snapshot and optional cache refresh |
| `internal/store/sqlite` + `migrations/core` | SQLite lifecycle and ordered transactional migrations |
| `internal/injectscan` + `internal/platform/pathsecurity` | Instruction scanning and skill-linked-file containment |

Use concrete types, small interfaces at the consumer, explicit errors and `context.Context` for cancellable work. No hidden global orchestration. All model-requested effects pass through `tools.Registry.Execute`; filtering the advertised list alone is insufficient. Plan cannot write files or memory, run shell or invoke MCP. Child agents inherit plan; explore children are always read-only; all children are leaves.

Timeouts, bounded file reads/tool output, process-tree cancellation and optional file hashes remain. Agent paths may be absolute and outside the launch directory; do not recreate workspace grant policy. The OS defines agent access.

## Runtime and data

- Config: `$YMZ_HOME` or `~/.yunmengze` on every OS. Prefer `agent.local.json` over `agent.json`. Never load provider configuration from the project directory.
- `ymz` seeds missing config, `env` and `AGENTS.md`; never overwrites existing files. Nonempty process environment wins over `env`; API keys may use `{env:NAME}` or `{file:path}`.
- Configuration and secret values are snapshotted together at startup. Restart `ymz` after changing it; no file watcher or reload service.
- Model refs split on the first slash: `provider/model/with/slashes`. Optional `models` roles: `subagent`, `compact`, `web`, `vision`, `memory`. `/model` changes this session; the ready-screen selection carries to subsequent new sessions.
- ConfigDir `AGENTS.md` plus workspace `.yunmengze/AGENTS.md` are runtime instructions. This repository's root `AGENTS.md` is contributor guidance.
- Skills: `<config>/skills/<id>/SKILL.md` and `<workspace>/.yunmengze/skills/<id>/SKILL.md`; workspace wins. Bodies load through `/id` or `skills_list` / `skill_view`. Slash priority: builtin, `chat.commands`, skill.
- `task` creates or resumes a child session via `task_id`; kinds are general/explore/web. Only explicitly independent tasks may run concurrently, at most two; the parent integrates results. Parent history contains only the result; child messages remain separate and hidden from the normal session picker.
- `ask_user` is a synchronous TUI question with optional multiple selection; it is not an approval gate. CLI has no interactive question tool.
- Compaction changes the provider view; keep the full stored transcript. Cancellation must finish pending tool observations, stop subprocesses, release leases and join goroutines before closing SQLite.
- Persist UTC RFC3339Nano. Do not edit released migrations **001–029**; new changes append ordered migrations. Migration 030 imports old conversations and memory and removes retired feature tables. Migration 031 adds preference evidence/curation state; 032 stores canonical native compaction windows.
- Never commit secrets, user configs, actual databases, logs or `bin/`, `dist/`, `.cache/`.

Current documentation: [docs/wiki/README.md](docs/wiki/README.md). Keep the wiki aligned with current code, update pages in place, and link the implementation. Do not retain historical ADRs, superseded behavior or upgrade comparisons in the wiki. README is the project introduction, installation and quick-start entry point; keep both languages aligned. `docs/backlog/current.md` is the only living optimization list and contains outstanding work only. Released changelogs remain version-specific records outside the wiki.
