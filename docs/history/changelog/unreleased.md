# Unreleased (post v0.4.0)

Working notes for the next release. Promote into `docs/history/changelog/vX.Y.Z.md` at publish time ([`docs/release.md`](../../release.md)).

| Area | Change |
| --- | --- |
| Provider | Unset `contextWindow` fills from embedded [models.dev](https://models.dev) (`internal/modelcatalog`); miss → 1_048_576. Unset `maxTokens` omits `max_tokens` on OpenAI-compatible wires; Anthropic uses catalog or 128_000. Dropped the old `>16384 → 8192` packing clamp. OpenCode `limit.{context,output}` alias. Background refresh to `<ConfigDir>/cache/models.dev.json`. |
| Phase S | `task.kind` catalog (`general` / `explore` / `web`); children are always leaves (no nested `task`); unknown/unadvertised kind and forbidden tool requests fail closed without spawning a run. Chat Prefix lists extra configured `models.*` roles and no longer hard-codes “No vision”. |
| Phase W | `web_search` + `web_extract` builtins (ddg default; searxng/tavily via `chat.web`). Interactive agent `/perm` (similar = host); plan/cron never advertise. SSRF exception only for configured SearXNG host on `web_search`. `models.web` whitelist; `task(kind=web)` advertised. |
| Phase M | ADR-055 auxiliary media: `vision_analyze` (path or `/perm` url), `audio_transcribe` (Whisper multipart, not Complete), `video_analyze` (ffmpeg ≤8 frames). `models.vision`/`speech` whitelist; kinds advertised only when configured. `task(kind=speech)` child Role is `subagent`. Path/url `/perm` picks the matching plan scope. Audio/video input cap 16MiB. Main loop stays text. |
