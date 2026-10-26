# YunmengZe Agent

A coding agent for your terminal, written in Go. Explore a codebase, edit files and run commands through an interactive UI or a scriptable CLI.

[简体中文](README.zh.md) · [Documentation](docs/wiki/README.md) · [Releases](https://github.com/yyZe0122/YunmengZe-Agent/releases) · [Contributing](CONTRIBUTING.md)

## Features

- **Terminal workflow** — switch models, resume conversations, steer an active turn and answer questions without leaving the terminal.
- **Two execution modes** — inspect and plan with `plan`, or let `agent` edit files and execute commands.
- **Your choice of model** — connect OpenAI-compatible Chat Completions, Responses, Anthropic Messages or Gemini services, including compatible local endpoints.
- **Local conversation storage** — sessions, context summaries and memory share a SQLite database. Context compaction handles long tool runs; low-frequency memory curation keeps only evidenced user preferences.
- **Optional capabilities** — load file-based skills and MCP servers, run up to two independent child tasks, search the web and inspect images with the current or a separately configured vision model. Each capability can be disabled in configuration.
- **One executable** — `ymz` runs on Windows, Linux and macOS; no separate service is needed.

Conversation storage is local. Model calls and enabled web, image and MCP tools use the services you configure.

## Installation

Prebuilt releases support Windows, Linux and macOS on amd64 and arm64. Go is only required when building from source.

**Windows — [Scoop](https://scoop.sh):**

```powershell
scoop bucket add ymz https://github.com/yyZe0122/scoop-bucket
scoop install ymz/ymz
```

**macOS — [Homebrew](https://brew.sh):**

```sh
brew install --cask yyZe0122/tap/ymz
```

**Linux / macOS — user installer:**

```sh
curl -fsSL https://raw.githubusercontent.com/yyZe0122/YunmengZe-Agent/main/packaging/scripts/install-user.sh | sh
```

**Windows PowerShell — user installer without Scoop:**

```powershell
irm https://raw.githubusercontent.com/yyZe0122/YunmengZe-Agent/main/packaging/scripts/install.ps1 | iex
```

The user installers download the latest stable release, verify SHA-256 and preserve existing configuration. Reopen your terminal after installation, then run `ymz version`.

You can also download an archive from [Releases](https://github.com/yyZe0122/YunmengZe-Agent/releases), verify it against `checksums.txt`, and place `ymz` (`ymz.exe` on Windows) on your `PATH`.

<details>
<summary>Build from source</summary>

To build the current checkout, install **Go 1.26+** and clone the repository:

```sh
git clone https://github.com/yyZe0122/YunmengZe-Agent.git
cd YunmengZe-Agent
```

Linux / macOS:

```sh
make build
./bin/ymz
```

Windows PowerShell:

```powershell
./scripts/dev.ps1 -Action build
./bin/ymz.exe
```

</details>

See [installation](docs/wiki/installation.md) for updates, install locations and version selection. Check the version's release notes before upgrading an existing installation.

## Quick start

1. Run `ymz` once to create the configuration files in `~/.yunmengze` (`$env:USERPROFILE\.yunmengze` on Windows).
2. Edit `agent.json` to select your provider and model, and add the API key to the `env` file. The generated example uses `deepseek1/deepseek-chat` and `DEEPSEEK1_API_KEY`; the first launch reports a missing key until it is set.
3. Run `ymz config validate` to check local configuration, then start `ymz` from the project directory and enter a task. This check does not contact model services.

```sh
ymz
ymz --mode plan
ymz --continue
ymz run --mode plan "Explain how this project is organized"
ymz run --json "Summarize the changes in this workspace"
```

Use `ymz models` to list the configured main model, role models and provider catalog entries without requiring complete API credentials. Enabled preference memory refreshes automatically at the start of each run.

## Configuration

`YMZ_HOME` changes the configuration directory, which also holds `core.db`. `agent.local.json` takes precedence over `agent.json`; the two files are not merged. Start from the [configuration example](configs/agent.json.example) to use another provider or model.

The following switches default to `true`, including when omitted. Set a switch to `false` to disable that capability; existing sessions and memory remain stored.

| Capability | Configuration key |
| --- | --- |
| File skills | `chat.skills.enabled` |
| Preference memory | `chat.memory.enabled` |
| Child tasks | `chat.task.enabled` |
| Web search, extraction and web child tasks | `chat.web.enabled` |
| Image analysis | `chat.vision.enabled` |
| MCP integrations | `mcp.enabled` |
| Automatic context compaction | `chat.compaction.enabled` |

Manual `/compact` remains available when automatic compaction is off. Disabling web tools does not restrict networking by shell commands, MCP servers or image URLs.

Missing credential references block startup only for the main model or enabled capabilities. Other affected models appear unavailable and report the reason when selected. Configuration, resolved credentials and resolution errors are fixed at startup; restart after changing them. See [configuration](docs/wiki/configuration.md) for model roles, defaults and enabled-feature dependencies.

## Working with the agent

| Mode | Behavior |
| --- | --- |
| `agent` — default | Executes tools with your current OS account's access, including file changes and shell commands, without approval prompts. |
| `plan` | Allows inspection, search and discussion. The tool registry blocks file changes, shell, memory edits and MCP. |

In the terminal UI, press **Tab** to switch modes while idle, **Ctrl+L** to choose a model, and **Ctrl+S** to resume a session. **Enter** sends a message or queues steering during a run; **Esc** cancels the turn. `/help` lists commands.

For scripts, use `ymz run`. Put flags before the task text; `--json` emits one JSON event per line. See [CLI and TUI usage](docs/wiki/usage.md) for all commands and events, and [execution boundaries](SECURITY.md) for the scope of each mode.

## Documentation

- [Getting started](docs/wiki/installation.md) and [configuration](docs/wiki/configuration.md)
- [Tools](docs/wiki/tools.md), [skills](docs/wiki/skills.md) and [memory](docs/wiki/memory.md)
- [Architecture](docs/wiki/architecture.md), [provider adapters](docs/wiki/provider-protocols.md) and [database](docs/wiki/database.md)
- [Contributing](CONTRIBUTING.md) and [release process](docs/release.md)

CLI and TUI share one in-process Engine. Optional capabilities use explicit composition and startup switches; source removal also requires updating their configuration, prompts and UI entries. See the [module boundaries](docs/wiki/architecture.md#模块边界与可选能力) and [current review and optimization plan](docs/backlog/current.md).

The wiki describes the current checkout. For a packaged release, use the documentation at its matching Git tag.

## License

[Apache License 2.0](LICENSE).
