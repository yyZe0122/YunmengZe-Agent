# Release process

[scripts/publish-release.sh](../scripts/publish-release.sh) is the publishing entry point. The [PowerShell wrapper](../scripts/publish-release.ps1) delegates to it. The script locates its own checkout, uses the current OS account and never changes Git identity.

Publishing uses **GitHub Actions**: the local command validates the committed release, creates an annotated tag and atomically pushes main plus that tag. The [Release workflow](../.github/workflows/release.yml) builds and uploads through the same script. Only the workflow uploads, so local and Actions publishers cannot race.

This public repository uses the standard ubuntu-latest runner, whose execution is free for public repositories under [GitHub's current runner policy](https://docs.github.com/en/actions/reference/runners/github-hosted-runners). GoReleaser's open-source distribution is used. No paid runner or GoReleaser Pro feature is configured.

## Prepare and publish

1. Review changes and commit by feature. The script does not stage, commit, stash, merge or force-push.
2. Write and commit the chosen version's notes at docs/history/changelog/vX.Y.Z.md. The file must contain at least 400 bytes of meaningful notes and the exact headings shown below. Keep previously released notes unchanged.
3. Move completed entries out of [unreleased.md](history/changelog/unreleased.md), leaving its heading and an empty Highlights section, and include that reset in the notes commit.
4. Run the preview, resolve its blockers, then run the publishing command. Replace vX.Y.Z with the version you chose.

~~~markdown
# YunmengZe Agent vX.Y.Z

## Highlights

Describe the user-visible changes and any migration requirements.

## Assets

Linux/macOS/Windows, amd64 and arm64: ymz archives and checksums.txt.
~~~

Windows needs Git for Windows, including Git Bash, and working Git push credentials. The PowerShell wrapper locates Git Bash even when Git was installed through Scoop. Normal publishing does not require local Go, make, GoReleaser, gh, administrator privileges or a manually supplied upload token.

~~~powershell
# Preview: local checks only; no network, file writes, tags or push.
.\scripts\publish-release.ps1 vX.Y.Z -DryRun

# Publish: push main + tag, then let Actions build and upload.
.\scripts\publish-release.ps1 vX.Y.Z
~~~

Linux/macOS use Bash and Git:

~~~sh
bash scripts/publish-release.sh vX.Y.Z --dry-run
bash scripts/publish-release.sh vX.Y.Z
~~~

The command must run from a clean main checkout whose origin push URL points to this repository. Notes must already be committed. A local tag on another commit, an existing remote tag, tracked ignored files or a CI skip marker in HEAD stops the release. The script does not run local tests. Its success means the tag was pushed; confirm the linked Release workflow finishes successfully before treating the release as published.

Do not put [skip ci], [ci skip], [skip actions], [actions skip], [no ci] or skip-checks: true in the release commit. These markers also suppress the tag-triggered workflow.

## Checks and local snapshots

| Purpose | Linux/macOS | Windows |
| --- | --- | --- |
| Format check and vet; no tests | make lint | ./scripts/dev.ps1 -Action lint |
| Format check, vet and tests, when requested | make check | ./scripts/dev.ps1 -Action check |
| Validate committed release inputs only | bash scripts/publish-release.sh vX.Y.Z --check | ./scripts/publish-release.ps1 vX.Y.Z -Check |
| Build snapshot archives without publishing | bash scripts/publish-release.sh --snapshot-only | ./scripts/publish-release.ps1 -SnapshotOnly |

Snapshot mode needs Go 1.26+ and GoReleaser v2. It accepts a dirty checkout, does not need versioned notes, and replaces the generated dist/ directory. Its version comes from the current Git state; omit the version argument. GORELEASER_PARALLELISM controls build parallelism and defaults to 1.

The Release workflow checks notes, dependency hashes, module consistency, formatting, vet, tests, script syntax and GoReleaser configuration before publishing. Its GoReleaser version is pinned in the workflow. General CI runs for branch pushes and pull requests; a tag uses the Release workflow instead of duplicating general CI.

Coding agents must run or add tests only when explicitly requested. The workflow's configured checks do not authorize running tests locally.

## Assets and authentication

GoReleaser builds only cmd/ymz with CGO_ENABLED=0:

~~~text
ymz_VERSION_linux_amd64.tar.gz
ymz_VERSION_linux_arm64.tar.gz
ymz_VERSION_darwin_amd64.tar.gz
ymz_VERSION_darwin_arm64.tar.gz
ymz_VERSION_windows_amd64.zip
ymz_VERSION_windows_arm64.zip
checksums.txt
~~~

Each archive contains ymz (or ymz.exe), both READMEs, LICENSE, config example/schema and user installers. Snapshot/build output stays ignored under dist/ and bin/. Archives use an explicit file list so local configs, data, caches and private release plans are not packaged.

The workflow uses its automatic GITHUB_TOKEN with contents write permission for this repository's Release. Local Git push access is separate; a credential used to push workflow changes must also allow those changes.

Homebrew Cask and Scoop updates are optional. To enable them, configure the repository's Actions secret PACKAGE_GITHUB_TOKEN with Contents write access to both yyZe0122/homebrew-tap and yyZe0122/scoop-bucket. Without that secret, GoReleaser skips those two uploads and still publishes the GitHub archives. It never reuses the repository-scoped GITHUB_TOKEN for other repositories. Tags with a prerelease suffix also skip package updates.

Plain vX.Y.Z tags publish as stable GitHub releases; tags with a prerelease suffix such as -rc.1 publish as prereleases. The script passes the exact versioned file as release notes and verifies the uploaded body and all seven nonempty assets. Installers verify SHA-256 before copying and preserve existing configuration; see [installation](wiki/installation.md).

## Failure and retry

- A failed atomic push leaves the remote main and tag unchanged by that push. A new local tag may remain; after fixing access or branch divergence, retry only if it still points at the reviewed HEAD.
- Once a remote tag exists, the script refuses to push it again. Open [Release workflow runs](https://github.com/yyZe0122/YunmengZe-Agent/actions/workflows/release.yml) and rerun the failed job for that tag. Re-pushing an unchanged tag does not start another run.
- Workflow retries use the code and notes at the original tag. Code changes need a new version. No force-tag or tag-deletion option is provided.
- Release and package uploads are not one transaction. A failure may leave partial remote results; inspect the job and release before retrying. GitHub release immutability or other repository policies may prevent replacing published assets.

Keep personal plans, unpublished notes, temporary files and backups under .cache/. Existing local helpers there are not the supported publishing entry point. Never commit tokens or real configs. Root env, agent.json, .yunmengze/, databases and sidecars are ignored; sanitized config examples remain under configs/.
