#!/usr/bin/env bash
# One-shot GitHub Release publisher for this host (scheme A: root only).
#
# Default path: push main/tag (if needed) → local GoReleaser build + upload
# (does not rely on GitHub Actions minutes / billing).
#
# Usage (as root) — full runbook: docs/release.md
#   cd /home/yyze/projects/AutoZeAgent
#   # require docs/history/changelog/vX.Y.Z.md first (GitHub Release body; stub fails)
#   # working tree MUST be clean (batch-commit features yourself)
#   ./scripts/publish-release.sh v0.4.0 --yes                       # clean main → tag+upload
#   ./scripts/publish-release.sh v0.3.0 --commit-paths changelog --yes  # leftover notes only
#   ./scripts/publish-release.sh v0.3.0 --upload-only               # tag already on HEAD
#   ./scripts/publish-release.sh v0.3.0 --snapshot-only             # no tag
#   ./scripts/publish-release.sh v0.3.0 --dry-run
#
# Requires: git, make, goreleaser; root only (scheme A).
#   GITHUB_TOKEN or gh auth login  — main Release upload
#   PACKAGE_GITHUB_TOKEN           — homebrew-tap + scoop-bucket (falls back to GITHUB_TOKEN)
set -euo pipefail

REPO_DEFAULT="/home/yyze/projects/AutoZeAgent"
REMOTE="origin"
BRANCH="main"
GIT_USER_NAME="yyZe"
GIT_USER_EMAIL="yyze@debianze.local"
GITHUB_REPO_SLUG="yyZe0122/YunmengZe-Agent"

TAG=""
COMMIT_PATHS="" # empty | changelog
DRY_RUN=0
SKIP_CHECK=0
SKIP_SNAPSHOT=0
SNAPSHOT_ONLY=0
UPLOAD_ONLY=0
VIA_ACTIONS=0
FORCE_TAG=0
YES=0
COMMIT_MSG=""
TAG_MSG=""
REPO_DIR=""
PARALLELISM="${GORELEASER_PARALLELISM:-1}"

usage() {
  cat <<'EOF'
One-shot release (root only). Default: local GoReleaser upload via GITHUB_TOKEN.

  ./scripts/publish-release.sh v0.4.0 --yes
  ./scripts/publish-release.sh v0.4.0 --commit-paths changelog --yes
  ./scripts/publish-release.sh v0.4.0 --upload-only
  ./scripts/publish-release.sh v0.4.0 --via-actions
  ./scripts/publish-release.sh v0.4.0 --dry-run

Options:
  --repo DIR            Repository root (default: /home/yyze/projects/AutoZeAgent)
  --commit-paths MODE   changelog = leftover docs/history/changelog + unreleased only (needs --yes)
  --message TEXT        Commit message when committing
  --tag-message TEXT    Annotated tag message
  --skip-check          Skip make check
  --skip-snapshot       Skip snapshot preflight before real release
  --snapshot-only       make check + snapshot only (no tag/upload)
  --upload-only         HEAD already tagged; only goreleaser release (no push/tag)
  --via-actions         Push tag only; let GitHub Actions publish (needs billing OK)
  --force-tag           Delete local+remote tag if present, then recreate
  --dry-run             Print steps only
  --yes                 Confirm dangerous modes
  -h, --help            This help

Environment:
  GITHUB_TOKEN            Required for default local upload (repo write / contents)
  PACKAGE_GITHUB_TOKEN    Required to push Homebrew formula + Scoop manifest
                          (Contents R/W on yyZe0122/homebrew-tap and scoop-bucket).
                          Falls back to GITHUB_TOKEN if unset (only works if that
                          token can write both affiliate repos).
  GITHUB_REPOSITORY       Optional owner/name (default yyZe0122/YunmengZe-Agent)
  GORELEASER_PARALLELISM  Default 1
EOF
}

log() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
run() {
  if [[ "$DRY_RUN" -eq 1 ]]; then
    printf '[dry-run] %s\n' "$*"
    return 0
  fi
  # shellcheck disable=SC2086
  eval "$@"
}

# Root login PATH often lacks Go (secure_path). Prepend known tool dirs before make/goreleaser.
ensure_toolchain_path() {
  local extra="" d
  for d in /usr/local/go/bin /home/yyze/go/bin /home/yyze/.local/bin /usr/local/bin; do
    if [[ -d "$d" ]]; then
      extra="${extra:+$extra:}$d"
    fi
  done
  if [[ -n "$extra" ]]; then
    PATH="${extra}:$PATH"
    export PATH
  fi
  if ! command -v go >/dev/null 2>&1; then
    die "go not found in PATH (looked in /usr/local/go/bin). Root login PATH is too thin; install Go or add it to PATH."
  fi
}

find_goreleaser() {
  if command -v goreleaser >/dev/null 2>&1; then
    command -v goreleaser
    return 0
  fi
  local c
  for c in /home/yyze/go/bin/goreleaser "${HOME}/go/bin/goreleaser" /usr/local/bin/goreleaser; do
    if [[ -x "$c" ]]; then
      echo "$c"
      return 0
    fi
  done
  return 1
}

find_gh() {
  if command -v gh >/dev/null 2>&1; then
    command -v gh
    return 0
  fi
  local c
  for c in /usr/local/bin/gh /home/yyze/.local/bin/gh; do
    if [[ -x "$c" ]]; then
      echo "$c"
      return 0
    fi
  done
  return 1
}

# GitHub Release body = docs/history/changelog/${TAG}.md.
# goreleaser --release-notes is skipped when changelog.disable is true (v2 Skip);
# always write the body with gh after assets upload.
apply_release_notes() {
  local gh_bin body
  gh_bin=$(find_gh) || die "gh not found; cannot write Release body from ${NOTES}"
  log "set GitHub Release body from ${NOTES}"
  if [[ "$DRY_RUN" -eq 1 ]]; then
    log "would run: gh release edit ${TAG} --notes-file ${NOTES}"
    return 0
  fi
  GITHUB_TOKEN="${GITHUB_TOKEN}" GH_TOKEN="${GITHUB_TOKEN}" \
    "$gh_bin" release edit "$TAG" --repo "$GITHUB_REPOSITORY" --notes-file "$NOTES"
  body=$("$gh_bin" release view "$TAG" --repo "$GITHUB_REPOSITORY" --json body --jq .body 2>/dev/null || true)
  [[ -n "$body" ]] || die "GitHub Release ${TAG} body is still empty after --notes-file ${NOTES}"
  echo "$body" | grep -q "YunmengZe Agent ${TAG}" \
    || die "GitHub Release ${TAG} body does not match ${NOTES}"
  log "GitHub Release body matches ${NOTES}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    --repo) REPO_DIR=${2:-}; shift 2 ;;
    --commit-paths) COMMIT_PATHS=${2:-}; shift 2 ;;
    --message) COMMIT_MSG=${2:-}; shift 2 ;;
    --tag-message) TAG_MSG=${2:-}; shift 2 ;;
    --skip-check) SKIP_CHECK=1; shift ;;
    --skip-snapshot) SKIP_SNAPSHOT=1; shift ;;
    --snapshot-only) SNAPSHOT_ONLY=1; shift ;;
    --upload-only) UPLOAD_ONLY=1; shift ;;
    --via-actions) VIA_ACTIONS=1; shift ;;
    --force-tag) FORCE_TAG=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    --yes) YES=1; shift ;;
    -*)
      die "unknown option: $1"
      ;;
    *)
      if [[ -z "$TAG" ]]; then
        TAG=$1
        shift
      else
        die "unexpected argument: $1"
      fi
      ;;
  esac
done

[[ -n "$TAG" ]] || { usage >&2; die "tag required (e.g. v0.1.0)"; }

if [[ "$(id -u)" -ne 0 ]]; then
  die "must run as root (scheme A: only root commits/pushes/tags)"
fi

ensure_toolchain_path

[[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] \
  || die "tag must look like v0.1.0 or v0.1.0-alpha.1 (got: $TAG)"

if [[ "$SNAPSHOT_ONLY" -eq 1 && "$UPLOAD_ONLY" -eq 1 ]]; then
  die "use only one of --snapshot-only / --upload-only"
fi
if [[ "$VIA_ACTIONS" -eq 1 && "$UPLOAD_ONLY" -eq 1 ]]; then
  die "--via-actions and --upload-only are mutually exclusive"
fi

REPO_DIR=${REPO_DIR:-$REPO_DEFAULT}
cd "$REPO_DIR" || die "cannot cd $REPO_DIR"
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || die "not a git repository: $REPO_DIR"

NOTES="docs/history/changelog/${TAG}.md"
assert_release_notes() {
  local notes=$1
  [[ -f "$notes" ]] || die "missing release notes: $notes — write docs/history/changelog/${TAG}.md before publishing (GitHub Release body; see docs/release.md)"
  local bytes
  bytes=$(wc -c < "$notes" | tr -d ' ')
  [[ "$bytes" -ge 400 ]] || die "release notes too short ($bytes bytes): $notes — bilingual changelog required, not an empty stub or git log"
  grep -q "^# YunmengZe Agent ${TAG}$" "$notes" \
    || die "release notes title must be '# YunmengZe Agent ${TAG}'"
  grep -q "## Highlights" "$notes" \
    || die "release notes must include ## Highlights (this file is the GitHub Release body)"
  grep -q "## Assets" "$notes" \
    || die "release notes must include ## Assets"
}
assert_release_notes "$NOTES"

current_branch=$(git rev-parse --abbrev-ref HEAD)
[[ "$current_branch" == "$BRANCH" ]] || die "on branch '$current_branch', expected '$BRANCH'"

export GITHUB_REPOSITORY="${GITHUB_REPOSITORY:-$GITHUB_REPO_SLUG}"

# Local git identity (this repo only)
if [[ -z "$(git config --local user.name 2>/dev/null || true)" ]]; then
  log "set local user.name=$GIT_USER_NAME"
  run "git config user.name \"$GIT_USER_NAME\""
fi
if [[ -z "$(git config --local user.email 2>/dev/null || true)" ]]; then
  log "set local user.email=$GIT_USER_EMAIL"
  run "git config user.email \"$GIT_USER_EMAIL\""
fi

assert_no_secrets_staged() {
  local bad
  bad=$(git diff --cached --name-only 2>/dev/null | grep -iE 'local\.json$|\.db$|\.jsonl$|permissions-trust|credentials\.json|\.pem$|id_rsa|id_ed25519|\.env$' || true)
  if [[ -n "$bad" ]]; then
    printf '%s\n' "$bad" >&2
    die "refusing to commit sensitive paths (see above)"
  fi
}

commit_changelog_paths() {
  [[ "$YES" -eq 1 ]] || die "--commit-paths changelog requires --yes"
  log "stage leftover changelog only"
  local paths=(
    "docs/history/changelog/${TAG}.md"
    docs/history/changelog/unreleased.md
  )
  local p
  for p in "${paths[@]}"; do
    if [[ -e "$p" ]] || git ls-files --error-unmatch "$p" >/dev/null 2>&1; then
      run "git add -- \"$p\""
    fi
  done
  assert_no_secrets_staged
  extra=$(git diff --cached --name-only | grep -vE '^docs/history/changelog/' || true)
  if [[ -n "$extra" ]]; then
    printf '%s\n' "$extra" >&2
    die "--commit-paths changelog staged unexpected paths"
  fi
  if git diff --cached --quiet 2>/dev/null; then
    log "nothing to commit on changelog leftover"
    return 0
  fi
  local msg=${COMMIT_MSG:-"docs(changelog): ${TAG}"}
  if [[ "$DRY_RUN" -eq 1 ]]; then
    log "would commit: $msg"
    git diff --cached --stat || true
    return 0
  fi
  git commit -m "$msg"
  log "committed changelog leftover"
}

case "$COMMIT_PATHS" in
  "") ;;
  changelog) commit_changelog_paths ;;
  all|release)
    die "--commit-paths ${COMMIT_PATHS} removed; batch-commit features yourself (see docs/release.md)"
    ;;
  *) die "--commit-paths must be 'changelog' (leftover notes only)" ;;
esac

# Dirty tree (snapshot-only may still want clean for goreleaser; enforce for publish)
if [[ "$SNAPSHOT_ONLY" -eq 0 ]]; then
  dirty=$(git status --porcelain --untracked-files=normal 2>/dev/null || true)
  if [[ -n "$dirty" ]]; then
    printf '%s\n' "$dirty" >&2
    die "working tree not clean; batch-commit features first (see docs/release.md). Only leftover notes: --commit-paths changelog --yes"
  fi
fi

if [[ "$SKIP_CHECK" -eq 0 ]]; then
  log "make check"
  if [[ "$DRY_RUN" -eq 1 ]]; then
    log "would run: make check"
  else
    make check
  fi
else
  log "skip make check"
fi

GR=""
if GR=$(find_goreleaser); then
  :
else
  GR=""
fi

if [[ "$SKIP_SNAPSHOT" -eq 0 && -n "$GR" ]]; then
  log "goreleaser snapshot preflight ($GR)"
  if [[ "$DRY_RUN" -eq 1 ]]; then
    log "would run: $GR release --snapshot --clean --parallelism ${PARALLELISM}"
  else
    "$GR" release --snapshot --clean --parallelism "$PARALLELISM"
    log "snapshot archives:"
    ls -1 dist/ymz_* 2>/dev/null | head -20 || true
  fi
elif [[ "$SKIP_SNAPSHOT" -eq 0 ]]; then
  log "goreleaser not found; skip snapshot preflight"
fi

if [[ "$SNAPSHOT_ONLY" -eq 1 ]]; then
  log "snapshot-only done (no tag/upload)"
  exit 0
fi

ensure_tag_on_head() {
  local head tag_commit
  head=$(git rev-parse HEAD)
  if git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null 2>&1; then
    tag_commit=$(git rev-parse "${TAG}^{}")
    if [[ "$tag_commit" != "$head" ]]; then
      if [[ "$FORCE_TAG" -eq 1 ]]; then
        [[ "$YES" -eq 1 ]] || die "tag ${TAG} points elsewhere; --force-tag requires --yes"
        log "move local tag ${TAG} to HEAD"
        run "git tag -d \"$TAG\""
      else
        die "local tag ${TAG} points to $tag_commit, HEAD is $head (use --force-tag --yes)"
      fi
    else
      log "local tag ${TAG} already on HEAD"
      return 0
    fi
  fi
  TAG_MSG=${TAG_MSG:-"YunmengZe ${TAG}"}
  log "create annotated tag ${TAG}"
  run "git tag -a \"$TAG\" -m \"$TAG_MSG\""
}

push_main_and_tag() {
  log "push ${BRANCH} to ${REMOTE}"
  run "git push \"$REMOTE\" \"$BRANCH\""

  if git ls-remote --tags "$REMOTE" "refs/tags/${TAG}" 2>/dev/null | grep -q .; then
    remote_commit=$(git ls-remote --tags "$REMOTE" "refs/tags/${TAG}" | awk '{print $1}' | head -1)
    # annotated tags show peeled in ls-remote with ^{}; compare peeled if possible
    if [[ "$FORCE_TAG" -eq 1 ]]; then
      [[ "$YES" -eq 1 ]] || die "remote tag exists; --force-tag requires --yes"
      log "delete remote tag ${TAG}"
      run "git push \"$REMOTE\" \":refs/tags/${TAG}\""
    else
      # If remote tag already exists, still push only if we recreated local — require force to replace
      log "remote tag ${TAG} already exists (leave in place; re-upload uses same tag)"
    fi
  fi

  # Ensure remote has our tag
  if ! git ls-remote --tags "$REMOTE" "refs/tags/${TAG}" 2>/dev/null | grep -q .; then
    log "push tag ${TAG}"
    run "git push \"$REMOTE\" \"$TAG\""
  elif [[ "$FORCE_TAG" -eq 1 ]]; then
    log "push tag ${TAG} (after force delete)"
    run "git push \"$REMOTE\" \"$TAG\""
  else
    # verify remote points at same commit as local
    local_peeled=$(git rev-parse "${TAG}^{}")
    # fetch remote peeled is hard without network object; push --force only with force-tag
    log "remote tag ${TAG} present; not force-updating (use --force-tag --yes to replace)"
  fi
}

if [[ "$UPLOAD_ONLY" -eq 0 ]]; then
  ensure_tag_on_head
  push_main_and_tag
else
  log "upload-only: skip push main (unless retagging)"
  head=$(git rev-parse HEAD)
  if git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null 2>&1; then
    tag_commit=$(git rev-parse "${TAG}^{}")
    if [[ "$head" != "$tag_commit" ]]; then
      if [[ "$FORCE_TAG" -eq 1 ]]; then
        [[ "$YES" -eq 1 ]] || die "tag ${TAG} is on $tag_commit, HEAD is $head; --force-tag requires --yes"
        log "move tag ${TAG} to HEAD and push"
        run "git tag -d \"$TAG\""
        TAG_MSG=${TAG_MSG:-"YunmengZe ${TAG}"}
        run "git tag -a \"$TAG\" -m \"$TAG_MSG\""
        if git ls-remote --tags "$REMOTE" "refs/tags/${TAG}" 2>/dev/null | grep -q .; then
          run "git push \"$REMOTE\" \":refs/tags/${TAG}\""
        fi
        run "git push \"$REMOTE\" \"$TAG\""
      else
        die "HEAD ($head) != ${TAG} ($tag_commit). Either: git checkout ${TAG}  OR  re-run with --force-tag --yes to move ${TAG} to HEAD and push"
      fi
    else
      log "local tag ${TAG} already on HEAD"
    fi
  else
    if [[ "$FORCE_TAG" -eq 1 ]] || [[ "$YES" -eq 1 ]]; then
      TAG_MSG=${TAG_MSG:-"YunmengZe ${TAG}"}
      log "create tag ${TAG} on HEAD (upload-only)"
      run "git tag -a \"$TAG\" -m \"$TAG_MSG\""
      run "git push \"$REMOTE\" \"$TAG\""
    else
      die "upload-only requires local tag ${TAG} on HEAD (or --force-tag --yes)"
    fi
  fi
fi

VER_NUM=${TAG#v}

if [[ "$VIA_ACTIONS" -eq 1 ]]; then
  log "via-actions: tag pushed; GitHub Actions must build (needs billing OK)"
  cat <<EOF

==> tag ${TAG} pushed for Actions

  Actions:  https://github.com/${GITHUB_REPOSITORY}/actions
  Release:  https://github.com/${GITHUB_REPOSITORY}/releases/tag/${TAG}

  If you only see Source code zip/tar.gz, the Release workflow failed
  (e.g. billing lock). Prefer default local upload instead of --via-actions.

  Expected assets after a green Release job:
    ymz_${VER_NUM}_linux_amd64.tar.gz
    ymz_${VER_NUM}_linux_arm64.tar.gz
    ymz_${VER_NUM}_darwin_amd64.tar.gz
    ymz_${VER_NUM}_darwin_arm64.tar.gz
    ymz_${VER_NUM}_windows_amd64.zip
    ymz_${VER_NUM}_windows_arm64.zip
    checksums.txt
    ymz-vscode_${VER_NUM}.vsix
    ymz-vscode-tui_${VER_NUM}.vsix
EOF
  exit 0
fi

# --- Default: local GoReleaser upload ---
[[ -n "$GR" ]] || GR=$(find_goreleaser) || die "goreleaser not found (install: go install github.com/goreleaser/goreleaser/v2@latest)"

# Prefer explicit GITHUB_TOKEN; else use `gh auth token` after `gh auth login` (root).
if [[ -z "${GITHUB_TOKEN:-}" ]]; then
  GH_BIN=""
  if command -v gh >/dev/null 2>&1; then
    GH_BIN=$(command -v gh)
  elif [[ -x /usr/local/bin/gh ]]; then
    GH_BIN=/usr/local/bin/gh
  elif [[ -x /home/yyze/.local/bin/gh ]]; then
    GH_BIN=/home/yyze/.local/bin/gh
  fi
  if [[ -n "$GH_BIN" ]] && "$GH_BIN" auth status >/dev/null 2>&1; then
    tok=$("$GH_BIN" auth token 2>/dev/null || true)
    if [[ -n "$tok" ]]; then
      export GITHUB_TOKEN="$tok"
      log "using token from: $GH_BIN auth token"
    fi
  fi
fi
if [[ -z "${GITHUB_TOKEN:-}" ]]; then
  die "No GITHUB_TOKEN. As root: install gh, run 'gh auth login' (repo+workflow scopes), then re-run. Or: export GITHUB_TOKEN=... See docs/release.md."
fi

# Homebrew/Scoop push token (separate affiliate repos). Fall back to GITHUB_TOKEN
# only when that token can write homebrew-tap + scoop-bucket (e.g. classic repo scope).
if [[ -z "${PACKAGE_GITHUB_TOKEN:-}" ]]; then
  export PACKAGE_GITHUB_TOKEN="${GITHUB_TOKEN}"
  log "PACKAGE_GITHUB_TOKEN unset; using GITHUB_TOKEN for brew/scoop push"
else
  log "using PACKAGE_GITHUB_TOKEN for brew/scoop push"
fi

log "local goreleaser release + upload ($GR)"
if [[ "$DRY_RUN" -eq 1 ]]; then
  log "would run: GITHUB_TOKEN=*** PACKAGE_GITHUB_TOKEN=*** $GR release --clean --parallelism ${PARALLELISM} --release-notes=${NOTES}"
else
  # Tag must be reachable; goreleaser uses git describe
  git describe --tags --exact-match HEAD >/dev/null 2>&1 \
    || die "HEAD is not exactly tag ${TAG}; checkout the tagged commit"
  set +e
  "$GR" release --clean --parallelism "$PARALLELISM" --release-notes="$NOTES"
  gr_ec=$?
  set -e
  if [[ "$gr_ec" -ne 0 ]]; then
    cat <<'EOF' >&2

error: goreleaser upload failed (often HTTP 403 on POST .../releases).

Fix the token, then re-run (packaging already works):
  ./scripts/publish-release.sh vX.Y.Z --upload-only --skip-check --skip-snapshot

Token checklist:
  Fine-grained PAT (recommended):
    - Resource owner = your user (or org that owns the repo)
    - Repository access = Only select → YunmengZe + homebrew-tap + scoop-bucket
    - Permissions → Repository → Contents: Read and write
    - Permissions → Repository → Workflows: Read and write
      (REQUIRED on YunmengZe if the tagged commit changes .github/workflows/*;
       otherwise POST /releases returns 403 "Resource not accessible...")
    - Permissions → Repository → Metadata: Read-only (default)
    - If the org uses SAML SSO: Authorize the token for that org
  Or split tokens:
    - GITHUB_TOKEN → YunmengZe (Contents + Workflows)
    - PACKAGE_GITHUB_TOKEN → homebrew-tap + scoop-bucket (Contents R/W)
  Classic PAT:
    - Scopes: repo + workflow  (workflow is not optional when release.yml changed)
  Do not use a read-only or Actions-only token.
  Do not paste the token into git or chat.

  Probe create-release permission (expect 201 or 422, not 403):
    curl -sS -o /tmp/rel_probe.json -w "%{http_code}\\n" \\
      -X POST -H "Authorization: Bearer $GITHUB_TOKEN" \\
      -H "Accept: application/vnd.github+json" \\
      https://api.github.com/repos/yyZe0122/YunmengZe-Agent/releases \\
      -d '{"tag_name":"v0.0.0-permcheck","name":"permcheck","draft":true,"prerelease":true}'
    # 201 = ok (then DELETE the draft); 403 = fix token; 422 = often tag/name clash but auth ok

Test API access (should return 200, not 403/401):
  curl -sS -o /dev/null -w "%{http_code}\n" \
    -H "Authorization: Bearer $GITHUB_TOKEN" \
    -H "Accept: application/vnd.github+json" \
    https://api.github.com/repos/yyZe0122/YunmengZe-Agent
EOF
    exit "$gr_ec"
  fi
fi

apply_release_notes

resolve_vsix() {
  local name="$1"
  if [[ -f "extensions/vscode/${name}" ]]; then
    echo "extensions/vscode/${name}"
    return 0
  fi
  if [[ -f "dist/vscode/${name}" ]]; then
    echo "dist/vscode/${name}"
    return 0
  fi
  return 1
}

upload_vscode_vsix() {
  local vsix tui owner pkg_ec names
  vsix="ymz-vscode_${VER_NUM}.vsix"
  tui="ymz-vscode-tui_${VER_NUM}.vsix"
  log "package VS Code VSIX (${TAG}) — GUI + TUI required on every tag"
  if [[ "$DRY_RUN" -eq 1 ]]; then
    log "would run: scripts/package-vscode.sh ${TAG} && gh release upload ${TAG} ${vsix} ${tui}"
    return 0
  fi
  owner=$(stat -c '%U' "$REPO_DIR" 2>/dev/null || echo yyze)
  set +e
  if [[ "$(id -u)" -eq 0 && "$owner" != "root" ]]; then
    su -s /bin/sh "$owner" -c "cd '$REPO_DIR' && sh ./scripts/package-vscode.sh '$TAG'"
    pkg_ec=$?
  else
    sh ./scripts/package-vscode.sh "$TAG"
    pkg_ec=$?
  fi
  set -e
  if [[ "$pkg_ec" -ne 0 ]]; then
    die "VS Code VSIX package failed (exit ${pkg_ec}; need Node 18+ as user ${owner}). Go assets may already be on the Release. Fix Node, then: ./scripts/publish-release.sh ${TAG} --upload-only --skip-check --skip-snapshot"
  fi
  vsix=$(resolve_vsix "$vsix") || die "missing ymz-vscode_${VER_NUM}.vsix after package-vscode.sh"
  tui=$(resolve_vsix "$tui") || die "missing ymz-vscode-tui_${VER_NUM}.vsix after package-vscode.sh"
  GH_BIN=$(find_gh) || die "gh not found; cannot upload required VSIX"
  log "upload ${vsix} ${tui} to ${TAG}"
  GITHUB_TOKEN="${GITHUB_TOKEN}" GH_TOKEN="${GITHUB_TOKEN}" "$GH_BIN" release upload "$TAG" "$vsix" "$tui" --repo "$GITHUB_REPOSITORY" --clobber
  names=$("$GH_BIN" release view "$TAG" --repo "$GITHUB_REPOSITORY" --json assets --jq '.assets[].name' 2>/dev/null || true)
  echo "$names" | grep -q "ymz-vscode_${VER_NUM}.vsix" \
    || die "GitHub Release ${TAG} missing asset ymz-vscode_${VER_NUM}.vsix"
  echo "$names" | grep -q "ymz-vscode-tui_${VER_NUM}.vsix" \
    || die "GitHub Release ${TAG} missing asset ymz-vscode-tui_${VER_NUM}.vsix"
  log "GitHub Release includes ymz-vscode_${VER_NUM}.vsix and ymz-vscode-tui_${VER_NUM}.vsix"
}

upload_vscode_vsix

cat <<EOF

==> local publish finished for ${TAG}

  Release:  https://github.com/${GITHUB_REPOSITORY}/releases/tag/${TAG}
  Homebrew: https://github.com/yyZe0122/homebrew-tap (Casks/ymz.rb)
  Scoop:    https://github.com/yyZe0122/scoop-bucket (agent.json)

  Assets (Pre-release):
    ymz_${VER_NUM}_linux_amd64.tar.gz
    ymz_${VER_NUM}_linux_arm64.tar.gz
    ymz_${VER_NUM}_darwin_amd64.tar.gz
    ymz_${VER_NUM}_darwin_arm64.tar.gz
    ymz_${VER_NUM}_windows_amd64.zip
    ymz_${VER_NUM}_windows_arm64.zip
    checksums.txt
    ymz-vscode_${VER_NUM}.vsix       # required GUI; missing fails the publish
    ymz-vscode-tui_${VER_NUM}.vsix   # required TUI; missing fails the publish

  Body: ${NOTES}
  Install (recommended):
    brew install --cask yyZe0122/tap/ymz
    scoop bucket add ymz https://github.com/yyZe0122/scoop-bucket && scoop install ymz
  Fallback installer: YMZ_VERSION=${TAG}

  unset GITHUB_TOKEN PACKAGE_GITHUB_TOKEN   # when done
EOF
