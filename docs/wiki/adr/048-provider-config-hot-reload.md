# ADR-048: Provider config hot-reload

## Status

Accepted (2026-08-12)

## Context

Operators change `agent.json` / `agent.local.json` / `env` while `ymzd` runs. Restarting for every key or model tweak is noisy. Constraints:

- Gateway must not call providers or hold secrets in responses
- Main stack may switch (`SetProvider` / `SetModel`); role map and chat policy stay start-time
- Incomplete config must not block Gateway; chat may be absent until a successful start
- No late-bind of `agent` / `chatsession` after a failed start (composition root stays simple)

## Decision

### Watch

`internal/configreload` watches **ConfigDir** only for basenames:

- `agent.json`, `agent.local.json`, `env`
- known editor temps (`*.tmp`, `*~`, `.*.swp`)

Debounce ~500ms; OnChange coalesced (one in flight; dirty re-run). Panic in OnChange is recovered and logged.

### Apply

`internal/providerruntime` owns main provider load, fingerprint, reload, and `SelectModel`:

1. `providerconfig.Load` (loads `env` first; **process non-empty env wins**)
2. Fingerprint of resolved config: non-secret fields + **hash** of API key and headers (never log raw secrets)
3. Unchanged fingerprint → skip
4. Else `providers.NewConfigured` → `agent.SetProvider` / `SetModel` / `SetContextWindow` + chat window
5. Failure: keep previous client (in-flight runs); set `loadError`; `ready=false` on GET `/v1/config/model`

`/model` (`WriteSelectedModel`) sets a short suppress window so the watcher does not double-apply.

### Ready semantics

`ready` means the model stack is usable for **chat and switch**:

- config load OK **and** agent was bound at daemon start
- If daemon started without agent (bad/missing config), hot-reload may clear `loadError` for listing but **chat stays unavailable** until `ymz restart` — `SelectModel` returns unavailable with restart guidance

### Not hot-reloaded

| Area | Action |
| --- | --- |
| `chat.*` | restart |
| MCP registration | restart |
| `models.subagent` / `models.compact` (ADR-045) | restart |
| Process env already set when using `{env:VAR}` | change process env + restart (or use literal / `{file:}`) |

### Key rotation that works without restart

- Literal `apiKey` in JSON
- `{file:…}` content change
- `{env:VAR}` when VAR was empty and `env` file supplies it on Load

## Consequences

- Main model/baseURL/key (as above) update within ~0.5s; next turn uses new client
- Startup misconfig still requires restart after fix
- CLI: `ymz start|stop|restart|status` and `ymz daemon …` (including `restart`)

## Related

- ADR-037 lifecycle, ADR-045 roles, `docs/wiki/provider-protocols.md`, `internal/providerruntime`, `internal/modelcatalog` (models.dev window fill; background refresh does not block Gateway)
