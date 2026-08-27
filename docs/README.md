# Documentation

Catalog for YunmengZe Agent docs. Start here.

| Box | Path | Role |
| --- | --- | --- |
| **Wiki** | [`wiki/`](wiki/) | Stable design: ADRs, `core.db` map, protocols, verify notes |
| **History** | [`history/changelog/`](history/changelog/) | Per-tag release notes + `unreleased.md` |
| **Backlog** | [`backlog/current.md`](backlog/current.md) | **Only** living status: in-progress / later / deferred |

Ops (not a box): [`release.md`](release.md) — only publish runbook.

User install/run: [`README.md`](../README.md) · [`README.zh.md`](../README.zh.md). Agent constraints: [`AGENTS.md`](../AGENTS.md). PR norms: [`CONTRIBUTING.md`](../CONTRIBUTING.md).

## Wiki

| Page | Use when |
| --- | --- |
| [`wiki/README.md`](wiki/README.md) | ADR start-here + numbered index |
| [`wiki/database.md`](wiki/database.md) | `core.db` live tables (not column dump) |
| [`wiki/adr/`](wiki/adr/) | Numbered decision records |
| [`wiki/provider-protocols.md`](wiki/provider-protocols.md) | Provider / `chat.*` wire fields |
| [`wiki/security/linux-sandbox-roadmap.md`](wiki/security/linux-sandbox-roadmap.md) | Linux isolation phases |
| [`wiki/testing/scheduler.md`](wiki/testing/scheduler.md) | In-process job verify |
| [`wiki/testing/skills.md`](wiki/testing/skills.md) | File-skill verify |
| [`wiki/vscode.md`](wiki/vscode.md) | VS Code / Cursor VSIX install + Release asset |

## History

Write `history/changelog/vX.Y.Z.md` before publish. Tag name must match the file. **That file is the GitHub Release body** (empty stub fails). Working notes go in [`unreleased.md`](history/changelog/unreleased.md); promote at tag time. Do not rewrite published notes.

## Backlog

[`backlog/current.md`](backlog/current.md) is the only living optimization file. Landed detail belongs in ADRs, changelog, and git. Do not add sibling status notes.

## Conventions

- New architecture decision → numbered ADR under `wiki/adr/` + update `wiki/README.md`.
- New / changed live table → update `wiki/database.md`; SQL in `migrations/core/` stays the source of truth.
- User-facing behavior → also update root `README.md` and `README.zh.md`.
- Missing ADR numbers are historical gaps — do not recreate.
