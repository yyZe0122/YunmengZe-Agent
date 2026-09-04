# Provider protocol configuration

YunmengZe selects a wire protocol from each provider's JSON configuration. The provider ID and URL do not imply a protocol, so the same gateway URL can expose different adapters by changing only `type` or `protocol`.

Configuration is loaded **only** from the OS config directory (`paths.Layout.ConfigDir`):

1. `<config-dir>/agent.local.json` (machine-local; preferred)
2. `<config-dir>/agent.json`
3. Optional `<config-dir>/env` — `KEY=value` lines loaded into the process before resolving `{env:…}` (does **not** override variables already set in the environment)

| Mode | Path |
| --- | --- |
| user (all OS) | `~/.yunmengze` (`%USERPROFILE%\.yunmengze` on Windows); override with `YMZ_HOME` |
| system | Linux `/etc/yunmengze` · Windows `ProgramData\YunmengZe\config` · macOS system path from `paths` |

On first start, if ConfigDir has no file, the daemon writes a default template with `{env:…}` placeholders (no secrets), top-level `models.subagent` / `models.compact` pointing at the same ref as `model`, and may seed an empty `env` template plus `AGENTS.md`. It does **not** copy project/cwd configs and does **not** write a `chat` / `mcp` block (runtime defaults apply). Installers do the same without overwriting existing files.

`agent.local.json` **replaces** `agent.json` (not a merge). Project directories are not searched for JSON. User-facing field table: [README · Configure](../../README.md#configure). JSON Schema: [`configs/agent.schema.json`](../../configs/agent.schema.json).

### Import from OpenCode

```bash
ymz config import-opencode [path] [--mode user|system] [--dry-run] [--output path]
```

Default path (when omitted): `~/.config/opencode/opencode.json` then `~/.opencode/opencode.json` (and `.jsonc`). Writes `<config-dir>/agent.local.json` (`0600`). YunmengZe does **not** merge OpenCode global + project files — pass the file you want.

| Mapped | Notes |
| --- | --- |
| `model`, `provider.*` | Nested catalog; `npm` → `type` heuristic; options `baseURL` / `apiKey` / `headers` kept |
| `models.subagent` / `compact` / `web` / `vision` / `speech` | YMZ role map (provider/model strings). `models.main` and unknown keys dropped with warnings |
| Local stdio MCP | `command` string or argv array → `mcp.servers` (`type` stdio/local) |
| Remote MCP | `type` remote/sse/http or `url` → `mcp.servers` with `url` (+ `headers`); oauth block not imported |
| OC `command` map | → `chat.commands` (template / prompt / message + description) |
| `compaction` | Top-level OC `compaction.auto` / `enabled` → `chat.compaction.enabled` |

**Dropped with warnings:** plugins, LSP, theme/keybinds/tui, agent/mode maps, top-level `permission`/`tools`, `small_model` (use `models.compact`), `instructions`, MCP oauth, and other unknown top-level fields.

**Hand-fill after import:** `chat.workspace`, `chat.tools` / `chat.permission.allow`, extra role refs. Then `ymz config validate` and `ymz restart`. Implementation: `internal/opencodeimport`.

### Chat fields (`chat.*`)

Omit the whole `chat` object to keep defaults. Changing any `chat.*` key needs `ymz restart` (exception: interactive extra-root `/perm` permanent writes `chat.workspace.allow` in-memory).

| Field | Default | Notes |
| --- | --- | --- |
| `workspace.default` | `client_cwd` | `client_cwd` \| `daemon_cwd` \| absolute path. Session root = client launch cwd on submit |
| `workspace.allow` | `[]` | Extra absolute roots on the path ceiling |
| `workspace.allow_all` | `false` | Disables path-root containment (local single-user only) |
| `allow_write` | `true` | Agent write ceiling. Plan is always read-only |
| `tools.git` / `tools.process` | `false` | OR with `permission.allow`. Plan / cron never get these |
| `permission.mode` | — | Load-only `preauth`\|`ask`; ignored at runtime. Wait is TUI interactive |
| `permission.allow` | `[]` | Remember `process` and/or `git` so Agent does not prompt |
| `compaction.enabled` | `true` | Session head summarization |
| `max_iterations` | `0` | `0` = no hard step cap; `1`–`256` = last step is text-only soft landing |
| `memory.enabled` | `true` | In-process layered memory |
| `memory.max_inject_runes` | `2000` | Frozen system inject cap |
| `memory.session_search` | `true` | Transcript FTS tool |
| `memory.default_ttl` | off | Go duration for session/detail writes that omit `expires_at` |
| `memory.curator` | on when memory on | Post-turn LLM facts; `max_facts` 3; `timeout_ms` 15000; uses `models.compact` |
| `skills.unused_ttl` | off | Go duration; unused-but-once-used skills are soft-archived |
| `commands` | none | Slash templates; `$ARGUMENTS`; instruction only — no grants |
| `web.search` | `ddg` | `ddg` \| `searxng` \| `tavily`. searxng needs `searxng_url`; tavily needs `tavily_key` |

### MCP servers (`mcp.servers`)

| Transport | Config | Notes |
| --- | --- | --- |
| stdio | `command` + optional `args` / `env` | Default when `command` set; `type: "stdio"` or `"local"` |
| Streamable HTTP | `type: "http"`, `url`, optional `headers` | MCP 2025-03-26 streamable endpoint |
| Legacy SSE | `type: "sse"`, `url` | HTTP+SSE (2024-11-05) |
| Auto remote | `type: "remote"` or only `url` | Try streamable, fallback legacy SSE |

Header/env values support `{env:VAR}` / `{file:…}`. Gateway MCP status never returns URL or headers. MCP is **not** hot-reloaded (restart daemon). Schema: `mcp` in [`configs/agent.schema.json`](../../configs/agent.schema.json). Example: [`configs/agent.json.example`](../../configs/agent.json.example).

### Chat slash templates (`chat.commands`, O3)

```json
"chat": {
  "commands": {
    "review": {
      "description": "Code review focus",
      "template": "Review with security and API design in mind.\n\n$ARGUMENTS"
    }
  }
}
```

| Rule | Detail |
| --- | --- |
| Key | Slash name without `/`; `[a-zA-Z0-9_-]+`; must not clash with built-in TUI commands |
| `template` | Required; optional `$ARGUMENTS` / `$0`; injectscan at load; max 8000 runes |
| Effect | TUI expands → user message submit only — **no** grants / skills / policy |
| API | `GET /v1/config/commands` → `{commands:[{id,description,template}]}` |
| Reload | Not hot-reloaded (`chat.*`); `ymz restart` after edit |

### Chat loop cap (`chat.max_iterations`, ADR-052)

Omit or `0` = no hard step cap (Esc / 30min / token / loop-detect). `1`–`256` = last step is a text-only soft landing. Default is no hard cap.

### Chat web search (`chat.web`, Phase W)

```json
"chat": {
  "web": {
    "search": "ddg",
    "searxng_url": "{env:SEARXNG_URL}",
    "tavily_key": "{env:TAVILY_API_KEY}"
  }
}
```

| Field | Detail |
| --- | --- |
| `search` | `ddg` (default) \| `searxng` \| `tavily`. Omit `chat.web` → ddg |
| `searxng_url` | Required when `search` is searxng. `{env:}`/`{file:}` resolved at load. Host is the only SSRF exception, and only for `web_search` |
| `tavily_key` | Required when `search` is tavily. Never logged or served on Gateway |
| Tools | Interactive agent: `web_search` / `web_extract` / `http_get` wait `/perm` (similar = host). Plan / cron never advertise |
| Reload | `chat.*` — `ymz restart` |

## API keys (choose any; nothing is forced)

`options.apiKey` supports three forms:

| Form | Example | Notes |
| --- | --- | --- |
| Environment placeholder | `"{env:DEEPSEEK1_API_KEY}"` | **Recommended.** Value from process env and/or ConfigDir `env` file |
| File reference | `"{file:secrets/key.txt}"` | Path relative to ConfigDir, or absolute |
| Literal string | `"sk-..."` | Allowed for local convenience; protect file permissions; never commit |

Examples:

```json
"apiKey": "{env:DEEPSEEK1_API_KEY}"
```

```json
"apiKey": "{file:secrets/deepseek.key}"
```

```json
"apiKey": "sk-your-key-here"
```

Optional ConfigDir `env` file:

```bash
# ~/.yunmengze/env  (chmod 600)
DEEPSEEK1_API_KEY=sk-...
DEEPSEEK2_API_KEY=sk-...
```

See also [`configs/agent.json.example`](../../configs/agent.json.example) (includes env / file / literal illustrations).

## Multi-provider catalog (OpenCode-style nesting)

Each entry under `provider` is one **supplier** (endpoint + wire protocol + credentials). Its **model catalog is nested** under that entry.

Selection and wire ids follow **OpenCode** rules:

1. Top-level `model` is `providerID/modelID…` — only the **first** `/` separates supplier from model segment.
2. The model segment **may contain `/`** (OpenRouter / NewAPI style: `deepseek/deepseek-v4-flash`).
3. Catalog keys under `provider.<id>.models` must equal that model segment (exact match; may contain `/`).
4. The HTTP body `model` field is the **wire id**: `models.<key>.id` if set, otherwise the model segment (never the full selection string, never a stripped-down bare rewrite of a nested id).

| Concept | JSON path | Example |
| --- | --- | --- |
| Active selection | top-level `model` | `"deepseek1/deepseek-chat"` or `"deepseek2/deepseek/deepseek-v4-flash"` |
| Supplier deepseek1 | `provider.deepseek1` | official bare model ids |
| Supplier deepseek2 | `provider.deepseek2` | gateway nested wire ids / `id` override |
| Catalog key | `provider.<id>.models` | `"deepseek-chat"` or `"deepseek/deepseek-v4-flash"` |
| Wire override | `models.<key>.id` | `"flash": { "id": "deepseek/deepseek-v4-flash" }` |
| Role overrides | top-level `models` | `subagent` / `compact` / `web` / `vision` / `speech` → selection ref (ADR-045) — **not** the catalog |

Same model segment on two suppliers is fine; selection disambiguates. Templates use **`deepseek1`** / **`deepseek2`**:

```json
{
  "model": "deepseek1/deepseek-chat",
  "provider": {
    "deepseek1": {
      "type": "openai-compatible",
      "options": { "baseURL": "https://api.deepseek.com/v1", "apiKey": "{env:DEEPSEEK1_API_KEY}" },
      "models": { "deepseek-chat": { "name": "DeepSeek Chat" } }
    },
    "deepseek2": {
      "type": "openai-compatible",
      "options": { "baseURL": "https://llm.example.com/v1", "apiKey": "{env:DEEPSEEK2_API_KEY}" },
      "models": {
        "deepseek/deepseek-v4-flash": { "name": "Nested wire id" },
        "flash": { "name": "Flash alias", "id": "deepseek/deepseek-v4-flash" }
      }
    }
  }
}
```

- Prefer catalog keys equal to the upstream model id (including `/` when the gateway requires it). Optional `id` overrides the wire name while keeping a short selection key.
- Mistyped keys of the form `providerID/modelID` when the selection model segment is bare are still accepted as a convenience; wire id remains the bare segment (or `id`).
- Empty `models` under a provider allows any model id (pass-through; API may still reject unknown ids).
- TUI `/model` lists `providerId/modelId…` refs and sets **this session** (`PATCH` / submit `preferred_model`). `/model main provider/model` changes top-level **global** `model` (`PUT /v1/config/model`). TUI has no clear-prefer command (empty string only via `PATCH`).
- Session **preference** (O4): `PATCH /v1/sessions/{id}` `{preferred_model}` stores `metadata.model`. Ready-page pick is a **sticky** TUI draft (`/new` does not clear it; every new session from ready is written via `POST /v1/tasks` `{preferred_model}` before `StartChat`; not persisted across TUI restart). Chat runs resolve **job pin (H7) → prefer → main** via `internal/modelresolve` (invalid prefer falls back to main; invalid job pin fails start).
- **`ready`:** true only when config load succeeded **and** agent/chat was bound at daemon start. Otherwise `error` explains (fix config, or `ymz restart` if chat never started). Secrets never appear in `error`.

### Hot-reload (ADR-048)

While `ymzd` runs, edits to `agent.json` / `agent.local.json` / `env` rebuild the **main** provider client after ~500ms (`internal/providerruntime`). Fingerprint includes a hash of API key and headers (not plaintext in logs).

| Change | Hot-reload? |
| --- | --- |
| `model`, baseURL, protocol, maxTokens, contextWindow | Yes |
| models.dev catalog cache (window fill) | Background; next resolve / reload |
| Literal `apiKey` or `{file:…}` content | Yes |
| `{env:VAR}` via `env` file when process VAR is empty | Yes |
| Process env already set for `{env:VAR}` | **No** — change process env + restart |
| `chat.*`, MCP, `models.*` (`subagent` / `compact` / `web` / `vision` / `speech`) | **No** — `ymz restart` |
| Daemon started without agent (bad config) | Fix file then **`ymz restart`** (no late-bind) |

In-flight runs keep the previous client until the next turn.

### Role map (optional)

Top-level `models` maps roles to other **selection** refs (ADR-045). Unset roles fall back to `model`:

```json
{
  "model": "deepseek1/deepseek-chat",
  "models": {
    "subagent": "deepseek2/flash",
    "compact": "deepseek2/flash",
    "web": "deepseek2/flash"
  }
}
```

Allowed keys: `subagent`, `compact`, `web`, `vision`, `speech`. Do not set `models.main` or `models.video`. Unknown keys fail load. Changing `models.*` requires a daemon restart. The chat Prefix lists extra configured roles and does not hard-code “No vision”. `task.kind` advertises `general`/`explore`/`web` always; `vision`/`speech`/`video` only when the matching role is configured. Auxiliary media: see ADR-055.

## Protocol families and aliases

| Canonical protocol | Accepted `type` / `protocol` aliases | Default completion endpoint | Default API-key header |
| --- | --- | --- | --- |
| `openai-chat` | `openai-compatible`, `openai-compat`, `openai-chat-completions`, `chat-completions`, `ollama`, `lmstudio`, `llamacpp`, `vllm`, `litellm` | `/v1/chat/completions` | `Authorization: Bearer ...` |
| `openai-responses` | `openai`, `responses` | `/v1/responses` | `Authorization: Bearer ...` |
| `anthropic-messages` | `anthropic`, `anthropic-compatible`, `claude` | `/v1/messages` | `x-api-key: ...` |
| `gemini-generate-content` | `gemini`, `google`, `google-generative-ai` | `/v1beta/models/{model}:generateContent` | `x-goog-api-key: ...` |

If neither field is present, YunmengZe keeps backward compatibility by selecting `openai-chat`. If both `type` and `protocol` are present, they must resolve to the same canonical protocol.

Use `openai-compatible` for providers that expose Chat Completions. The `openai` alias intentionally selects the newer OpenAI Responses protocol.

## Common options

```json
{
  "options": {
    "baseURL": "https://gateway.example/v1",
    "apiKey": "{env:PROVIDER_API_KEY}",
    "completionPath": "/v1/chat/completions?api-version=2026-01-01",
    "modelsPath": "/v1/models",
    "headers": {
      "X-Organization": "{env:PROVIDER_ORG}",
      "api-key": "{file:.secrets/azure-key}"
    }
  }
}
```

- `baseURL` must be an absolute HTTP(S) URL without query parameters, fragments, or userinfo. A path prefix is allowed.
- `completionPath` and `modelsPath` must begin with `/` and may contain query parameters.
- If `baseURL` already ends in `/v1`, the default `/v1/...` endpoint does not duplicate that segment.
- `headers` are applied after protocol defaults, so an explicitly configured header can override a default authorization or version header.
- `apiKey` and every header value accept literal values, `{env:NAME}`, or `{file:path}`. Relative file paths are resolved from the JSON file's directory.
- `responseFormat` configures structured-output behavior only for `openai-chat`. It is optional; omit it to use automatic negotiation. `auto`, `json_schema`, and `json_object` are accepted as explicit values.
- `anthropicVersion` defaults to `2023-06-01`.
- Gemini `completionPath` must contain `{model}` because the model ID is part of the request URL.

## OpenAI-compatible Chat Completions

```json
{
  "model": "local/qwen3",
  "provider": {
    "local": {
      "type": "openai-compatible",
      "options": {
        "baseURL": "http://127.0.0.1:11434/v1"
      },
      "models": {
        "qwen3": { "name": "Qwen 3" }
      }
    }
  }
}
```

This protocol family covers OpenAI-compatible services such as DeepSeek, OpenRouter, Ollama, LM Studio, llama.cpp, vLLM, and LiteLLM when they expose `/chat/completions` semantics.

When a request includes a JSON Schema and `responseFormat` is omitted (or set to `auto`), YunmengZe first sends OpenAI's `json_schema` format. If the endpoint explicitly reports that `response_format` or `json_schema` is unsupported, YunmengZe retries once with `json_object` and remembers that choice for subsequent requests to the same model. Set `responseFormat` explicitly only when a gateway needs a fixed compatibility override or when debugging negotiation.

## Per-model generation options

Generation options belong to each entry in `models`, so models sharing a provider URL and API key can still use different defaults:

```json
{
  "models": {
    "chat-model": {
      "name": "Chat model",
      "temperature": 0.2,
      "maxTokens": 4096,
      "reasoningEffort": "high"
    }
  }
}
```

- `temperature` is used when the request does not already specify a temperature.
- `maxTokens` is a model-level output-token cap. When set it is sent on the wire and used as the packing output term. Omit it to leave OpenAI-chat / Responses / Gemini uncapped (`max_tokens` omitted); Anthropic still requires a value (catalog hit or **128_000**). It is not `plan.Budget.MaxTokens`.
- `contextWindow` is the model context length in tokens. It is **not** `maxTokens`. Used for **provider-view packing and TUI pressure** (ADR-041/051): usable window ≈ `contextWindow − packingOutput − reserve`. Omit or `0` to fill from the embedded **models.dev** catalog (`internal/modelcatalog`, `https://models.dev/models.json`); miss → **1_048_576**.
- `limit: { context, output }` is the OpenCode-style alias. Explicit `contextWindow` / `maxTokens` win when set.
- `reasoningEffort` is used when the request does not already specify an effort. It currently maps to `reasoning_effort` for OpenAI-compatible Chat Completions and to `reasoning.effort` for OpenAI Responses.
- Request-level values take precedence over model defaults, except that `maxTokens` always remains an upper bound.
- Configure only options supported by the selected model and endpoint. YunmengZe rejects `reasoningEffort` for the Anthropic and Gemini adapters rather than silently ignoring it.

## OpenAI Responses

```json
{
  "model": "openai/gpt-model-id",
  "provider": {
    "openai": {
      "type": "openai",
      "options": {
        "baseURL": "https://api.openai.com",
        "apiKey": "{env:OPENAI_API_KEY}"
      },
      "models": {
        "gpt-model-id": { "name": "OpenAI model" }
      }
    }
  }
}
```

## Anthropic Messages

```json
{
  "model": "anthropic/claude-model-id",
  "provider": {
    "anthropic": {
      "type": "anthropic",
      "options": {
        "baseURL": "https://api.anthropic.com",
        "apiKey": "{env:ANTHROPIC_API_KEY}",
        "anthropicVersion": "2023-06-01"
      },
      "models": {
        "claude-model-id": { "name": "Claude model" }
      }
    }
  }
}
```

## Google Gemini generateContent

```json
{
  "model": "google/gemini-model-id",
  "provider": {
    "google": {
      "type": "gemini",
      "options": {
        "baseURL": "https://generativelanguage.googleapis.com",
        "apiKey": "{env:GEMINI_API_KEY}"
      },
      "models": {
        "gemini-model-id": { "name": "Gemini model" }
      }
    }
  }
}
```

## Azure/OpenAI-compatible gateways

Use a custom endpoint query and header when the gateway does not accept bearer authentication:

```json
{
  "model": "azure/deployment-model-id",
  "provider": {
    "azure": {
      "type": "openai-compatible",
      "options": {
        "baseURL": "https://resource.openai.azure.com",
        "completionPath": "/openai/deployments/deployment-name/chat/completions?api-version=2026-01-01",
        "modelsPath": "/openai/models?api-version=2026-01-01",
        "headers": {
          "api-key": "{env:AZURE_OPENAI_API_KEY}"
        }
      },
      "models": {
        "deployment-model-id": { "name": "Azure deployment" }
      }
    }
  }
}
```

Do not also set `apiKey` when the service rejects the protocol's default authorization header; put the secret in `headers` instead.
