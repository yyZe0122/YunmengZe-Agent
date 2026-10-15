# Contributing

Read [AGENTS.md](AGENTS.md) and the [architecture guide](docs/wiki/architecture.md) before changing the runtime. CLI and TUI call the local Engine directly; features are composed through concrete services and small interfaces at their call sites.

## Development

Use Go 1.26+, `context.Context` for cancellable work, and explicit errors. Keep changes within the requested scope. Discuss uncertain design choices before adding abstractions or changing an agreed direction.

| Action | Linux / macOS | Windows PowerShell |
| --- | --- | --- |
| Format | `make format` | `./scripts/dev.ps1 -Action format` |
| Build | `make build` | `./scripts/dev.ps1 -Action build` |
| Format check and vet (no tests) | `make lint` | `./scripts/dev.ps1 -Action lint` |
| Format check, vet and tests | `make check` | `./scripts/dev.ps1 -Action check` |
| Check and build | `make all` | `./scripts/dev.ps1 -Action all` |

These commands are references. Coding agents must run or add tests only when the user explicitly requests them. The CI workflow runs its configured checks on pushes and pull requests.

After dependency changes, keep `go.mod` and `go.sum` consistent. Append new database migrations; never rewrite released SQL. Do not commit credentials, user configs, databases, logs or build output.

Keep personal release plans, notes and backups under the ignored `.cache/` directory. Root `env`, `agent.json`, `.yunmengze/` and database sidecars are ignored; share sanitized examples under `configs/` instead. Before committing, use `git diff --cached --check` and `git ls-files --cached --ignored --exclude-standard` to catch whitespace errors and tracked local files. Ignore rules do not remove files already tracked by Git.

A tool must declare its plan availability and execute through `tools.Registry.Execute`. Apply mode restrictions to child sessions and external integrations as well as the advertised tool list. Preserve cancellation, timeouts and the limits documented for each tool.

## Documentation

README introduces the project: capabilities, installation, quick start, documentation links and license. Keep English and Chinese READMEs aligned.

The wiki describes the current code only. Update the relevant page in place when a command, configuration field, default, tool, schema or runtime behavior changes. Link concrete source files, document actual limitations, and remove descriptions of features that no longer exist. Do not retain historical ADRs, superseded alternatives, upgrade comparisons or progress logs in the wiki.

Keep the configuration example and JSON schema consistent with the implementation. Use [docs/backlog/current.md](docs/backlog/current.md) only for outstanding work. Published changelogs remain version-specific records outside the wiki.

## Pull requests and releases

Describe the concrete problem, resulting behavior, validation actually performed and remaining limitations. Split independent features into reviewable commits.

Follow the [release process](docs/release.md). `scripts/publish-release.sh` owns publishing logic; Windows users call its `scripts/publish-release.ps1` wrapper. Local execution validates and pushes a tag, then GitHub Actions builds and uploads. Do not add a separate uploader or put CI skip markers in the release commit.
