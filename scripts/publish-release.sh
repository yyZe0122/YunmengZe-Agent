#!/usr/bin/env bash
# The publishing entry point. Local runs push a tag; Actions builds and uploads.
set -euo pipefail

# A non-login Git Bash started by PowerShell may omit its own Unix tools.
case "$OSTYPE" in
  msys*|cygwin*) export PATH="/usr/bin:/bin:$PATH" ;;
esac

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
REPOSITORY="yyZe0122/YunmengZe-Agent"
REMOTE="origin"
BRANCH="main"
TAG=""
MODE="release"
PARALLELISM="${GORELEASER_PARALLELISM:-1}"

log() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
require() { command -v "$1" >/dev/null 2>&1 || die "$1 is required."; }

usage() {
  cat <<'EOF'
One-command release through GitHub Actions. No root or local upload token needed.

  bash scripts/publish-release.sh vX.Y.Z
  bash scripts/publish-release.sh vX.Y.Z --dry-run
  bash scripts/publish-release.sh vX.Y.Z --check
  bash scripts/publish-release.sh --snapshot-only

Options (mutually exclusive):
  --dry-run        Show the plan and local blockers; no network or writes.
  --check          Validate the committed release notes and repository only.
  --snapshot-only  Build local archives in dist/; no tag, push or upload.
  --ci-publish     Internal: build/upload from the Release tag workflow only.
  -h, --help       Show help.

Normal release needs a clean main, committed versioned notes, and Git push access.
It creates an annotated tag and atomically pushes main + tag to origin.
It never commits files, changes Git identity, replaces tags, or runs local tests.
Snapshot mode needs Go 1.26+ and GoReleaser v2; normal release needs only Git.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    --dry-run|--check|--snapshot-only|--ci-publish)
      [[ "$MODE" == release ]] || die "choose only one mode"
      MODE="${1#--}"
      ;;
    -*) die "unknown option: $1 (see --help)" ;;
    *)
      [[ -z "$TAG" ]] || die "unexpected argument: $1"
      TAG="$1"
      ;;
  esac
  shift
done

cd -- "$ROOT"
require git
git rev-parse --is-inside-work-tree >/dev/null
[[ "$PARALLELISM" =~ ^[1-9][0-9]*$ ]] || die "GORELEASER_PARALLELISM must be positive."

if [[ "$MODE" == snapshot-only ]]; then
  [[ -z "$TAG" ]] || die "snapshot uses the current checkout; omit the version."
  if ! command -v go >/dev/null 2>&1 && [[ -d "$ROOT/.cache/toolchain/go/bin" ]]; then
    export PATH="$ROOT/.cache/toolchain/go/bin:$PATH"
  fi
  require go
  require goreleaser
  export CGO_ENABLED=0
  export PACKAGE_GITHUB_TOKEN="${PACKAGE_GITHUB_TOKEN:-}"
  goreleaser check
  goreleaser release --snapshot --clean --parallelism "$PARALLELISM"
  log "Snapshot archives are in dist/; nothing was published."
  exit 0
fi

[[ "$TAG" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$ ]] \
  || die "version must be vX.Y.Z or vX.Y.Z-rc.1"
NOTES="docs/history/changelog/${TAG}.md"
HEAD_COMMIT="$(git rev-parse HEAD)"
CI_TAG=0
if [[ "${GITHUB_ACTIONS:-}" == true && "${GITHUB_REF_TYPE:-}" == tag && "${GITHUB_REF_NAME:-}" == "$TAG" ]]; then
  CI_TAG=1
fi
if [[ "$MODE" == ci-publish && "$CI_TAG" -ne 1 ]]; then
  die "--ci-publish is reserved for the Release tag workflow."
fi

# Collect all local blockers so a dry run is useful before notes are ready.
BLOCKED=0
block() { printf 'blocked: %s\n' "$*" >&2; BLOCKED=1; }
branch="$(git symbolic-ref --quiet --short HEAD || true)"
if [[ "$CI_TAG" -ne 1 && "$branch" != "$BRANCH" ]]; then
  block "release from main (current branch: ${branch:-detached HEAD})."
fi
if [[ -n "$(git --no-optional-locks status --porcelain --untracked-files=all)" ]]; then
  block "working tree is not clean; review and commit changes by feature first."
fi
ignored="$(git ls-files --cached --ignored --exclude-standard)"
if [[ -n "$ignored" ]]; then
  printf '%s\n' "$ignored" >&2
  block "tracked files match .gitignore; remove local data from Git before release."
fi
if [[ ! -f "$NOTES" ]]; then
  block "missing $NOTES."
else
  [[ "$(wc -c < "$NOTES")" -ge 400 ]] || block "$NOTES needs at least 400 bytes of meaningful notes."
  # Accept CRLF worktrees; the committed release file is normalized to LF.
  notes_text="$(tr -d '\r' < "$NOTES")"
  grep -Fxq "# YunmengZe Agent $TAG" <<< "$notes_text" || block "notes title must be '# YunmengZe Agent $TAG'."
  grep -Fxq "## Highlights" <<< "$notes_text" || block "notes need a '## Highlights' section."
  grep -Fxq "## Assets" <<< "$notes_text" || block "notes need an '## Assets' section."
  git cat-file -e "HEAD:$NOTES" 2>/dev/null || block "$NOTES must be committed."
fi
if git log -1 --format=%B | grep -Eiq '\[(skip ci|ci skip|no ci|skip actions|actions skip)\]|skip-checks:[[:space:]]*true'; then
  block "HEAD has a CI skip marker; its tag would not trigger the Release workflow."
fi
local_tag="$(git rev-parse --verify --quiet "refs/tags/${TAG}^{commit}" || true)"
if [[ -n "$local_tag" && "$local_tag" != "$HEAD_COMMIT" ]]; then
  block "local $TAG points at a different commit; use a new version."
fi
if [[ "$CI_TAG" -eq 1 && "$local_tag" != "$HEAD_COMMIT" ]]; then
  block "the workflow checkout must match $TAG."
fi
remote_url="$(git remote get-url --push --all "$REMOTE")"
case "$remote_url" in
  "https://github.com/$REPOSITORY"|"https://github.com/$REPOSITORY.git"|"git@github.com:$REPOSITORY.git"|"ssh://git@github.com/$REPOSITORY.git") ;;
  *) block "origin push URL must point to $REPOSITORY on github.com." ;;
esac

if [[ "$MODE" == dry-run ]]; then
  log "Repository: $ROOT"
  log "Version: $TAG; commit: $HEAD_COMMIT; notes: $NOTES"
  log "Plan: validate -> annotated tag -> atomic push of main + tag -> Release workflow."
  log "Actions builds six archives + checksums.txt and verifies the release body/assets."
  log "No local files, Git state or remote services were changed."
  exit "$BLOCKED"
fi
[[ "$BLOCKED" -eq 0 ]] || die "resolve the blockers above before releasing."
if [[ "$MODE" == check ]]; then
  log "Release inputs are valid for $TAG (local validation only)."
  exit 0
fi

if [[ "$MODE" == ci-publish ]]; then
  require go
  require goreleaser
  require gh
  [[ -n "${GITHUB_TOKEN:-}" ]] || die "the workflow must supply GITHUB_TOKEN."
  export GH_TOKEN="$GITHUB_TOKEN"
  export CGO_ENABLED=0
  export GORELEASER_CURRENT_TAG="$TAG"
  # An absent cross-repository token skips only Homebrew/Scoop in GoReleaser.
  export PACKAGE_GITHUB_TOKEN="${PACKAGE_GITHUB_TOKEN:-}"
  if [[ -z "$PACKAGE_GITHUB_TOKEN" ]]; then
    log "PACKAGE_GITHUB_TOKEN is unset; publishing GitHub assets only."
  fi
  goreleaser release --clean --parallelism "$PARALLELISM" --release-notes "$NOTES"
  body="$(gh release view "$TAG" --repo "$REPOSITORY" --json body --jq .body)"
  expected="$(cat "$NOTES")"
  [[ "$body" == "$expected" ]] || die "published release body differs from $NOTES."
  assets="$(gh release view "$TAG" --repo "$REPOSITORY" --json assets --jq '.assets[] | select(.size > 0) | .name')"
  for platform in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64 windows_arm64; do
    extension=tar.gz
    [[ "$platform" != windows_* ]] || extension=zip
    asset="ymz_${TAG#v}_${platform}.${extension}"
    grep -Fxq "$asset" <<< "$assets" || die "release is missing a nonempty $asset."
  done
  grep -Fxq checksums.txt <<< "$assets" || die "release is missing nonempty checksums.txt."
  log "Published and verified https://github.com/$REPOSITORY/releases/tag/$TAG"
  exit 0
fi

# An unchanged remote tag push does not start another workflow.
remote_tag="$(git ls-remote --tags "$remote_url" "refs/tags/$TAG")"
[[ -z "$remote_tag" ]] || die "remote $TAG already exists. Re-run its failed Release workflow, or use a new version for code changes."
if [[ -z "$local_tag" ]]; then
  git var GIT_COMMITTER_IDENT >/dev/null
  git tag -a "$TAG" -m "YunmengZe Agent $TAG"
fi
git push --atomic "$REMOTE" "HEAD:refs/heads/$BRANCH" "refs/tags/$TAG:refs/tags/$TAG"
log "Tag pushed. GitHub Actions will build and upload the release."
log "Progress: https://github.com/$REPOSITORY/actions/workflows/release.yml"
log "Release appears after a successful workflow: https://github.com/$REPOSITORY/releases/tag/$TAG"
