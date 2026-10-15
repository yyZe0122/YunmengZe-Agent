# Windows convenience wrapper; all release logic lives in publish-release.sh.
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string]$Version,
    [switch]$DryRun,
    [switch]$Check,
    [switch]$SnapshotOnly,
    [switch]$Help
)

$ErrorActionPreference = 'Stop'
$modes = @($DryRun, $Check, $SnapshotOnly) | Where-Object { $_ }
if (@($modes).Count -gt 1) { throw 'Choose only one of -DryRun, -Check and -SnapshotOnly.' }

# Resolve Git for Windows, including Scoop shims, without accidentally using WSL.
$git = Get-Command git -ErrorAction Stop
$execPath = & $git.Source --exec-path
if ($LASTEXITCODE -ne 0) { throw 'Could not locate Git.' }
$gitRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $execPath))
$bash = Join-Path $gitRoot 'bin/bash.exe'
if (-not (Test-Path -LiteralPath $bash)) {
    if ($env:OS -eq 'Windows_NT') { throw 'Git Bash was not found. Install Git for Windows with Git Bash.' }
    $bash = (Get-Command bash -ErrorAction Stop).Source
}

$arguments = @((Join-Path $PSScriptRoot 'publish-release.sh').Replace('\', '/'))
if ($Version) { $arguments += $Version }
if ($DryRun) { $arguments += '--dry-run' }
if ($Check) { $arguments += '--check' }
if ($SnapshotOnly) { $arguments += '--snapshot-only' }
if ($Help) { $arguments += '--help' }

& $bash --noprofile --norc @arguments
if ($LASTEXITCODE -ne 0) { throw "Release command stopped (exit $LASTEXITCODE). See the messages above." }
