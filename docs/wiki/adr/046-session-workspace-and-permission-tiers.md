# ADR-046: Session workspace + permission tiers

## Status

Accepted (implemented 2026-08-10; extra-root `/perm` 2026-09-02)

## Context

Auto-started `ymzd` used `DataDir` as cwd, so tools saw DataDir instead of the directory where the user ran `ymz`. Interactive permissions (ADR-043) only offered once/similar/deny, with no permanent trust table.

## Decision

### Session workspace

1. Client (TUI/CLI) sends absolute `workspace` on task submit (= `os.Getwd()`).
2. Daemon stores it in `sessions.metadata.workspace` when ensuring the session.
3. Chat plan Paths / grants use **session workspace** (plus configured `chat.workspace.allow` and session `metadata.extra_roots`).
4. Shared `PathGuard` starts with config ceiling; `AddRoot(session workspace)` on chat auth. Extra-root `/perm` does **not** always `AddRoot`: once is call-scoped, similar is session-scoped, only permanent raises the process ceiling.
5. `chat.workspace.allow_all=true` disables path-root containment (local single-user only; audited).
6. Interactive extra-root `/perm`: once this call (`AddOnceRoot`); similar this session (`metadata.extra_roots` + `AddSessionRoot`); permanent writes `agent.local.json` `chat.workspace.allow` and patches in-memory ChatConfig (no `ymz restart`).

Config (optional; defaults preserve client_cwd behavior when roots empty):

```json
"chat": {
  "workspace": {
    "default": "client_cwd",
    "allow": [],
    "allow_all": false
  },
  "permission": { "allow": [] }
}
```

### Permission decisions (ADR-043 extension)

| Decision | Meaning |
| --- | --- |
| `allow_once` | Single tool call (existing) |
| `allow_similar` | Session-scoped: same capability + path parent prefix; process args may **prefix-match** (ADR-051, e.g. `go test` ⊇ `go test ./foo`) |
| `allow_permanent` | Requires `confirm: true`; writes ConfigDir trust entry; future matches pre-grant |
| `deny` | Existing |

Jobs/cron still never wait. TUI Tab Auto writes `sessions.metadata.permission_stance=auto` (this session only; omit on later submit does not overwrite). `chat.permission.allow` remembers process/git without prompting. `chat.permission.mode` is load-only compatibility.

## Consequences

- Multi-project use of one daemon is correct when each session carries its launch cwd.
- PathGuard and grants stay aligned via shared expandable roots.
- Permanent trust is explicit and confirm-gated.

## References

- ADR-038, ADR-043, ADR-011, ADR-012, ADR-037
