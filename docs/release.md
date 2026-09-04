# Release and publication

日常安装/运行见根 [README](../README.md)。**本页是唯一发版操作手册**。不要另写平行步骤，不要发明第二套脚本。

This page is the **only** release runbook.

---

## Hard rules / 硬性规则

| Rule | Detail |
| --- | --- |
| **Root only** | On this host, **only root** may `git commit` / `git tag` / `git push` / upload. Agent or `yyze` prepares the tree; a human (or agent via `sudo -i`) publishes as root. |
| **One script** | Publish is **only** [`scripts/publish-release.sh`](../scripts/publish-release.sh). Delete leftover one-off publish helpers; do not add `scripts/release-*.sh`. |
| **Clean tree** | The publish script **refuses a dirty working tree**. Batch-commit first. It will not squash a multi-feature dump. |
| **No mega-commit** | **Never** `--commit-paths all` for a mixed dirty tree. That flag is **removed**. |
| **Changelog first** | `docs/history/changelog/vX.Y.Z.md` must exist **before** the publish command. Tag name = file name (`v0.4.0` → `v0.4.0.md`). That file **is** the GitHub Release body (`goreleaser --release-notes`). Empty stub / git log **fails**. |
| **VSIX required** | Every tag **must** attach `ymz-vscode_{version}.vsix` (Node 18+; fail-closed). Missing Node / missing file / failed upload **fails the publish**. |
| **No secrets** | Never commit `agent.local.json`, `*.db`, `env` with real keys, `bin/`, `dist/`, tokens. |

### Do not / 禁止

- Run `./scripts/publish-release.sh` as `yyze` or any non-root user.
- Invent a second publisher (`make release`, CI-only tag, ad-hoc `goreleaser` + `gh release create` mix).
- One-shot `git add -A && git commit -m "release: …"` across unrelated features.
- Force-push `main`. `--force-tag` only moves the **tag**, and still needs `--yes`.
- Rewrite published changelog files under `docs/history/changelog/v*.md`.

---

## Default path (this host) / 默认一键发版

### 1) Batch-commit as root (required)

Split the dirty tree into **feature-sized commits**. Each commit must compile. Run `make check` at least once before the first push.

```bash
sudo -i
cd /home/yyze/projects/AutoZeAgent

# inspect
git status
git diff --stat
git log --oneline -10

# one concern per commit — example only, adapt paths:
git add -- path/a path/b
git commit -m "feat(tui): …"

git add -- path/c
git commit -m "docs: …"

git push origin main
```

`--commit-paths changelog` on the publish script is **only** for a leftover that is already one logical change (typically `docs/history/changelog/vX.Y.Z.md` after feature commits are on `main`).

### 2) Write notes, then publish

```bash
sudo -i
cd /home/yyze/projects/AutoZeAgent

# Auth once per machine (preferred):
#   gh auth login
# Or for this shell only:
#   export GITHUB_TOKEN=...            # main repo Contents (+ Workflows if touching .github)
#   export PACKAGE_GITHUB_TOKEN=...    # homebrew-tap + scoop-bucket Contents R/W

# Changelog MUST exist: docs/history/changelog/vX.Y.Z.md
# Working tree MUST be clean (or only that changelog leftover).

./scripts/publish-release.sh vX.Y.Z --yes

# Changelog-only leftover (one commit), then tag + upload:
# ./scripts/publish-release.sh vX.Y.Z \
#   --commit-paths changelog --yes \
#   --message "docs(changelog): vX.Y.Z"
```

Replace `vX.Y.Z` (e.g. `v0.4.0`). The script **refuses** a missing or stub changelog. It runs `make check`, creates an annotated tag, pushes `main` + tag, then **local** `goreleaser release`. After Go assets upload it runs `gh release edit --notes-file docs/history/changelog/vX.Y.Z.md` (that markdown **is** the GitHub Release body), then packages and **must** upload `ymz-vscode_{version}.vsix`. Missing Node / missing VSIX / failed `gh release upload` **fails the publish** (Go archives may already be on the Release; re-run `--upload-only` after fixing Node). Details: [`wiki/vscode.md`](wiki/vscode.md).

### Pre-flight checklist

| Step | Action |
| --- | --- |
| 1 | **Batch-commit** by feature. Push those commits. |
| 2 | Write **`docs/history/changelog/vX.Y.Z.md`** (title `# YunmengZe Agent vX.Y.Z`, bilingual Highlights, Assets, Install). This file **is** the GitHub Release body. Missing / stub / no Highlights **fails** publish. |
| 3 | Reset `docs/history/changelog/unreleased.md` to an empty post-tag stub when promoting notes into `vX.Y.Z.md`. |
| 4 | No secrets in tree. |
| 5 | `make check` green (script runs it unless `--skip-check`). |
| 6 | Node 18+ available as the repo owner (root publish `su`s to that user to pack VSIX). Missing VSIX **fails**. |
| 7 | `gh auth login` or valid `GITHUB_TOKEN` + `PACKAGE_GITHUB_TOKEN`. |
| 8 | As **root**, clean tree: `./scripts/publish-release.sh vX.Y.Z --yes`. |

### After publish

```bash
gh release view vX.Y.Z --repo yyZe0122/YunmengZe-Agent
# Must list platform archives + checksums.txt + ymz-vscode_*.vsix (not only Source code zip)
# Body must be the changelog (title YunmengZe Agent vX.Y.Z), not empty / git log

gh api repos/yyZe0122/homebrew-tap/commits --jq '.[0].commit.message'
gh api repos/yyZe0122/scoop-bucket/commits --jq '.[0].commit.message'
```

Users:

```bash
brew upgrade --cask ymz
# or
export YMZ_VERSION=vX.Y.Z
curl -fsSL "https://raw.githubusercontent.com/yyZe0122/YunmengZe-Agent/main/packaging/scripts/install-user.sh" | sh
```

### Common variants

| Situation | Command |
| --- | --- |
| Only snapshot (no tag/upload) | `./scripts/publish-release.sh vX.Y.Z --snapshot-only` |
| Tag already on HEAD; re-upload assets | `./scripts/publish-release.sh vX.Y.Z --upload-only` |
| Move tag to current HEAD | `./scripts/publish-release.sh vX.Y.Z --force-tag --yes` |
| Print steps only | `./scripts/publish-release.sh vX.Y.Z --dry-run` |
| Push tag, let Actions publish | `./scripts/publish-release.sh vX.Y.Z --via-actions` (needs billing OK) |
| Leftover changelog commit | `./scripts/publish-release.sh vX.Y.Z --commit-paths changelog --yes --message "docs(changelog): vX.Y.Z"` |

Script: [`scripts/publish-release.sh`](../scripts/publish-release.sh).

---

## User install channels / 用户安装渠道

| Channel | Platforms | Command |
| --- | --- | --- |
| **Homebrew** (recommended) | macOS, Linux | `brew install --cask yyZe0122/tap/ymz` |
| **Scoop** (recommended) | Windows | `scoop bucket add ymz https://github.com/yyZe0122/scoop-bucket` then `scoop install ymz` |
| One-line scripts (fallback) | Win / Linux / macOS | `install.ps1` / `install-user.sh` |
| Manual / source | all | Release zip/tar or `make install` |
| **VS Code VSIX** | editor | `ymz-vscode_{version}.vsix` on the same Release — [`docs/wiki/vscode.md`](wiki/vscode.md) |

Affiliate repos (auto-updated by GoReleaser on each tag):

- [`yyZe0122/homebrew-tap`](https://github.com/yyZe0122/homebrew-tap) → `Casks/ymz.rb`
- [`yyZe0122/scoop-bucket`](https://github.com/yyZe0122/scoop-bucket)

## Asset naming / 资产命名

GoReleaser builds **one archive per OS/arch**. Each archive contains **two binaries** (`ymz`, `ymzd`) plus configs and packaging scripts.

| Pattern | Example (tag `v0.3.0`) |
| --- | --- |
| `ymz_{version}_{os}_{arch}.tar.gz` | `ymz_0.3.0_linux_amd64.tar.gz` |
| `ymz_{version}_windows_{arch}.zip` | `ymz_0.3.0_windows_amd64.zip` |
| `checksums.txt` | SHA-256 of all archives (fixed name) |
| `ymz-vscode_{version}.vsix` | VS Code / Cursor terminal launcher — **required** on every tag |

- `{version}` = tag **without** leading `v` (GoReleaser `.Version`).
- Prefer `YMZ_VERSION=vX.Y.Z` when the release is **Pre-release** (GitHub `latest` may skip it).

## Release notes / 更新日志

GitHub Release **必须**有更新日志正文。来源只有一份：`docs/history/changelog/vX.Y.Z.md`。

1. 发版前写好该文件（中英 Highlights、Assets、Install、Verify）。标题：`# YunmengZe Agent vX.Y.Z`。
2. Tag 名 = 文件名：`v0.4.0` → `docs/history/changelog/v0.4.0.md`。
3. `publish-release.sh` 上传资产后执行 `gh release edit --notes-file=该文件`（goreleaser `--release-notes` 在 `changelog.disable` 时会被 Skip，不可单独依赖）。脚本校验：文件存在、≥400 字节、标题、`## Highlights`、`## Assets`；再 `gh release view` 核对 body 非空且含标题。
4. 缺文件 / 空 stub / 无 Highlights → **拒绝发版**。不要用 git log 当 Release body。
5. 工作笔记在 [`unreleased.md`](history/changelog/unreleased.md)；打 tag 时 promote，并把 unreleased 重置为空 stub。已发布的 `v*.md` 不改写。

## Auth / 鉴权

**推荐（root）：`gh auth login`**，脚本会自动 `gh auth token` → `GITHUB_TOKEN`。

| Token | Required access |
| --- | --- |
| **`GITHUB_TOKEN`** | **YunmengZe-Agent**: Contents R/W; Workflows R/W if commit touches `.github/workflows` |
| **`PACKAGE_GITHUB_TOKEN`** | **homebrew-tap** + **scoop-bucket**: Contents R/W |
| **Classic** (one token) | **`repo` + `workflow`** |

If the Release page only shows **Source code** zip/tar.gz, binaries never uploaded — fix PAT and use default local upload (not `--via-actions` while billing is broken).

### Optional: GitHub Actions

[`.github/workflows/release.yml`](../.github/workflows/release.yml) runs on `v*` tags when Actions + billing are healthy. Prefer **local upload** when runners are unavailable. Repo secret `PACKAGE_GITHUB_TOKEN` required for brew/scoop from CI.

### Local snapshot only

```bash
export PACKAGE_GITHUB_TOKEN=dummy
goreleaser release --snapshot --clean --parallelism 1 --skip=publish
```

## Private-to-public audit checklist / 转公开清单

- Reachable branch/tag history is intentional; no legacy secrets in history.
- Secrets, local config, `*.db`, logs, caches untracked (root `.gitignore`).
- Full tests, `go mod verify`, installer checks, GoReleaser six-platform snapshot.
- Scan history, Actions logs, release notes, archives, `checksums.txt`.
- Authorized tester downloads every asset and smoke-tests.
- Review visibility settings before going public.

## Linux system install (brief)

```bash
sudo sh packaging/scripts/install.sh .
sudo install -m 0640 -o root -g ymz \
  configs/agent.json.example /etc/yunmengze/agent.json
sudo systemctl enable --now ymz
```

Details: [`packaging/install/systemd.md`](../packaging/install/systemd.md).
