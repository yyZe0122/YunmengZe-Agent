# ADR-045: Optional model roles (main / subagent / compact)

## Status

Accepted (2026-08-10). Updated 2026-09-04: TUI `/model` is this session; `/model main` is global. Updated 2026-08-28 (Phase S/W/M): Prefix lists extra configured roles; whitelist `subagent`/`compact`/`web`/`vision`/`speech`. `models.video` is never a key.

## Context

Provider config already holds a multi-model catalog (`provider.*.models`) and a single active selection (`model`). All LLM work — main chat, `task` sub-agents, and session compaction — used that one runner endpoint. Operators want Hermes-style cheap/fast models for sub-agents and compaction without losing a strong main model.

Constraints (unchanged):

- One daemon, one `core.db`, Gateway does not call providers
- Sub-agents remain logical child runs (ADR-039)
- Compaction remains dual-track durable transcript (ADR-041)
- Fail closed on invalid config

## Decision

### Config

Optional top-level map:

```json
{
  "model": "provider/main-model",
  "models": {
    "subagent": "provider/worker-model",
    "compact": "provider/cheap-model"
  }
}
```

| Field | Meaning |
| --- | --- |
| `model` | **Main** model (required). TUI `/model main` and `PUT /v1/config/model` change this. Bare TUI `/model` is session-scoped. |
| `models.subagent` | Optional. Used by `task` child runs (`kind` general/explore; `kind=web` falls back here when `models.web` is unset). |
| `models.compact` | Optional. Used by `CompactSummary*` / mid-turn compact LLM path. |
| `models.web` | Optional. Used by `task(kind=web)`. Omit → `subagent` then main. |
| `models.vision` | Optional. Auxiliary Complete for `vision_analyze` / `video_analyze`. Omit → tools not advertised. |
| `models.speech` | Optional. Whisper `/v1/audio/transcriptions` for `audio_transcribe` only — **not** a chat `RoleEndpoint`. `task(kind=speech)` still requires this key to advertise, then runs as `subagent`. Omit → tool not advertised. |

Rules:

- Omit `models` or a key → that role uses main
- First-start `EnsureConfig` seeds `models.subagent` and `models.compact` to the same ref as `model` (still optional to delete; omit ≡ main)
- Empty string value → treat as unset (fallback main)
- Values must be `provider/model` present in the catalog
- Allowed keys: `subagent`, `compact`, `web`, `vision`, `speech` (no `models.main`; unknown keys fail load). `validateModelsMap` error text comes from `AllowedModelRoles`. `models.video` is never a key (reuses vision).
- Changing `models.*` requires **daemon restart** (no hot rewrite). Main `model` / provider options may hot-reload (ADR-048); role endpoints are built only at start.

### Runtime

- Daemon builds optional role endpoints at startup (`ResolveModel` + `providers.NewConfigured` per distinct ref ≠ main). **`models.speech` is skipped** (Whisper is not Complete).
- `agent.Runner` holds main provider/model plus `map[role]RoleEndpoint`
- `RunRequest.Role`: empty/`main` → main; `subagent` when set by `task` tool; unconfigured → main
- `CompactSummary*` always selects role `compact` (fallback main)
- `SetProvider` / `SetModel` / `SetContextWindow` update **main only** (aligned with `/model main`)

### Session model preference + run-level resolve (O4)

Separate from role map: session row `metadata.model` (JSON) holds an optional **preference** string `provider/model`.

| Surface | Behavior |
| --- | --- |
| `PATCH /v1/sessions/{id}` `{ "preferred_model": "…" }` | Merge into metadata (preserves `workspace`); **empty string clears**. This is the only clear path — TUI has no `/model clear` |
| TUI `/model [provider/model]` · Ctrl+L | **This session**. Does **not** call `SelectModel` / rewrite config. Ready-page pick is a **sticky** TUI draft (`/new` does not clear it; every new session from ready writes it). Not persisted across TUI restart |
| TUI `/model main provider/model` | **Global** main (ADR-048 hot path). Does not rewrite other sessions’ prefer |
| `POST /v1/tasks` `{preferred_model}` | Written in `ensureSession` **before** `StartChat` so the first turn resolves it |
| Chat / agent run | **Resolve order:** job `model_ref` (H7, strict) → session prefer (if resolvable) → daemon main. Does **not** rewrite global config. Invalid session prefer → log + fall back to main; invalid job pin → fail start (no run) |
| Configured `Role` subagent/compact | Still wins over session prefer / job pin on those call sites (compact/subagent) |

Implementation: `internal/modelresolve` + `agent.RunRequest` override fields + `chatsession` (job pin + `PreferredModel`) before `agent.Run`.

Not a substitute for `models.subagent` / `models.compact`. H7 job model pin: `jobs.model_ref` + `schedulerapi` + `scheduledtasks` + same resolver (`ResolveStrict`).

### Out of scope

- Image/video generation, TTS (`models.tts`), TUI paste, and a multimodal main loop (ADR-055). Chat Prefix lists extra configured roles; it does not hard-code “No vision”.
- Per-call model in `task` tool input
- Gateway API for role map
- Hot-reload of `models.*`
- Concurrent multi-model UI beyond session prefer + global main (no split-pane)

## Consequences

- Operators can assign a cheaper model to compaction and sub-agents without switching the chat default mid-session via `/model main`
- Main switch remains explicit (`/model main`) and persistent
- Session `/model` applies per chat run without mutating global main; multiple TUIs can run different models
- Future roles: add whitelist key + one call-site `Role` assignment + schema property
- H7 pins jobs via `jobs.model_ref` and the same `modelresolve` path (strict on fire)

## References

- `internal/providerconfig` (`LoadModelRoles`, `validateModelsMap`)
- `internal/agent.Runner` (`Role`, `Roles`, `snapshotForRole`, model override, `ProposeMemoryFacts`)
- `internal/modelresolve`
- `internal/scheduler` / `pkg/schedulerapi` (`model_ref`)
- `internal/tools/task.go` / `taskkind.go` (`Role` from kind spec; general/explore → `subagent`)
- `cmd/ymzd` (`BuildRoleEndpoints`, `NewStoreWithMainRef`)
- ADR-039, ADR-041, ADR-042
