# Installs YunmengZe Agent user-wide on Windows (no admin).
# - Copies ymz.exe into %LOCALAPPDATA%\Programs\YunmengZe\bin
# - Adds that directory to the user PATH
# - Seeds %USERPROFILE%\.yunmengze\agent.json + env when missing (flat home root)
# apiKey may use {env:}, {file:}, or a literal string — none is forced.
param(
    [string]$Repository = $env:YMZ_REPOSITORY,
    [string]$Version = $env:YMZ_VERSION,
    [string]$InstallDir = $env:YMZ_INSTALL_DIR,
    [string]$ConfigDir = $env:YMZ_CONFIG_DIR
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($Repository)) {
    $Repository = 'yyZe0122/YunmengZe-Agent'
}

if ([string]::IsNullOrWhiteSpace($Version)) {
    $Version = 'latest'
}
if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        $InstallDir = Join-Path $env:USERPROFILE 'YunmengZe\bin'
    } else {
        $InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\YunmengZe\bin'
    }
}
if ([string]::IsNullOrWhiteSpace($ConfigDir)) {
    if (-not [string]::IsNullOrWhiteSpace($env:YMZ_HOME)) {
        $ConfigDir = $env:YMZ_HOME
    } else {
        $ConfigDir = Join-Path $env:USERPROFILE '.yunmengze'
    }
}

$architecture = $env:PROCESSOR_ARCHITEW6432
if ([string]::IsNullOrWhiteSpace($architecture)) {
    $architecture = $env:PROCESSOR_ARCHITECTURE
}
switch ($architecture.ToUpperInvariant()) {
    'AMD64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    default { throw "Unsupported Windows architecture: $architecture" }
}

function Get-ReleaseTag {
    param([string]$Repo, [string]$Requested)
    if ($Requested -ne 'latest') {
        return $Requested
    }
    $apiLatest = "https://api.github.com/repos/$Repo/releases/latest"
    try {
        $rel = Invoke-RestMethod -UseBasicParsing -Uri $apiLatest
        if (-not [string]::IsNullOrWhiteSpace($rel.tag_name)) {
            return [string]$rel.tag_name
        }
    } catch {
        # no non-prerelease latest
    }
    $apiList = "https://api.github.com/repos/$Repo/releases?per_page=20"
    try {
        $list = Invoke-RestMethod -UseBasicParsing -Uri $apiList
    } catch {
        throw "Could not list releases for $Repo. Set YMZ_VERSION=vX.Y.Z. $_"
    }
    foreach ($rel in $list) {
        if ($rel.draft) { continue }
        if (-not [string]::IsNullOrWhiteSpace($rel.tag_name)) {
            return [string]$rel.tag_name
        }
    }
    throw "No published releases for $Repo. Set YMZ_VERSION=vX.Y.Z."
}

function Install-UserConfig {
    param([string]$Dir, [string]$ExamplePath)
    New-Item -ItemType Directory -Force -Path $Dir | Out-Null
    $jsonPath = Join-Path $Dir 'agent.json'
    $localPath = Join-Path $Dir 'agent.local.json'
    $envPath = Join-Path $Dir 'env'
    if (-not (Test-Path -LiteralPath $jsonPath) -and -not (Test-Path -LiteralPath $localPath)) {
        if (-not [string]::IsNullOrWhiteSpace($ExamplePath) -and (Test-Path -LiteralPath $ExamplePath)) {
            $raw = Get-Content -LiteralPath $ExamplePath -Raw
            $raw = $raw -replace '(?m)^\s*"\$schema"\s*:[^\r\n]+[\r\n]*', ''
            Set-Content -LiteralPath $jsonPath -Value $raw -Encoding utf8
        } else {
            @'
{
  "model": "deepseek1/deepseek-chat",
  "provider": {
    "deepseek1": {
      "type": "openai-compatible",
      "options": {
        "baseURL": "https://api.deepseek.com/v1",
        "apiKey": "{env:DEEPSEEK1_API_KEY}"
      },
      "models": {
        "deepseek-chat": {
          "name": "DeepSeek Chat"
        }
      }
    }
  }
}
'@ | Set-Content -LiteralPath $jsonPath -Encoding utf8
        }
        Write-Host "  config: created $jsonPath"
    } else {
        Write-Host "  config: kept existing under $Dir"
    }
    if (-not (Test-Path -LiteralPath $envPath)) {
        @'
# Optional KEY=value (ymz load this; does not override process env already set).
# Pair with apiKey "{env:DEEPSEEK1_API_KEY}" / "{env:DEEPSEEK2_API_KEY}" in agent.json, or literal / {file:…}.
# Do not commit secrets.
#
DEEPSEEK1_API_KEY=
DEEPSEEK2_API_KEY=
OPENAI_API_KEY=
ANTHROPIC_API_KEY=
GEMINI_API_KEY=
'@ | Set-Content -LiteralPath $envPath -Encoding utf8
        Write-Host "  env: created $envPath (fill keys as needed)"
    } else {
        Write-Host "  env: kept existing $envPath"
    }
}

$tag = Get-ReleaseTag -Repo $Repository -Requested $Version
if ($tag.StartsWith('v')) {
    $verNum = $tag.Substring(1)
} else {
    $verNum = $tag
    $tag = "v$tag"
}

$asset = "ymz_${verNum}_windows_${arch}.zip"
$baseUrl = "https://github.com/$Repository/releases/download/$tag"

Write-Host "Installing YunmengZe $tag ($arch) ..."
Write-Host "  binaries → $InstallDir"
Write-Host "  config   → $ConfigDir"

$tempDir = Join-Path ([IO.Path]::GetTempPath()) ('ymz-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tempDir | Out-Null
try {
    $archivePath = Join-Path $tempDir $asset
    $checksumPath = Join-Path $tempDir 'checksums.txt'
    Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/$asset" -OutFile $archivePath
    Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/checksums.txt" -OutFile $checksumPath

    $expected = $null
    foreach ($line in Get-Content -LiteralPath $checksumPath) {
        $parts = $line.Trim() -split '\s+'
        if ($parts.Count -ge 2 -and $parts[$parts.Count - 1].TrimStart('*') -eq $asset) {
            $expected = $parts[0].ToLowerInvariant()
            break
        }
    }
    if ([string]::IsNullOrWhiteSpace($expected)) {
        throw "No checksum entry found for $asset."
    }

    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        throw "Checksum verification failed for $asset."
    }

    Expand-Archive -LiteralPath $archivePath -DestinationPath $tempDir -Force
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

    $bins = @('ymz.exe')
    foreach ($name in $bins) {
        $src = Join-Path $tempDir $name
        if (-not (Test-Path -LiteralPath $src)) {
            throw "Archive missing $name"
        }
        Copy-Item -LiteralPath $src -Destination (Join-Path $InstallDir $name) -Force
    }

    $example = Join-Path $tempDir 'configs\agent.json.example'
    if (-not (Test-Path -LiteralPath $example)) {
        $example = ''
    }
    Install-UserConfig -Dir $ConfigDir -ExamplePath $example

    if ($env:YMZ_NO_PATH_UPDATE -ne '1') {
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        $pathEntries = @()
        if (-not [string]::IsNullOrWhiteSpace($userPath)) {
            $pathEntries = $userPath.Split(';')
        }
        if ($pathEntries -notcontains $InstallDir) {
            $newUserPath = (($pathEntries + $InstallDir) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }) -join ';'
            [Environment]::SetEnvironmentVariable('Path', $newUserPath, 'User')
            Write-Host "  PATH (user): added $InstallDir"
        }
        if (($env:Path.Split(';')) -notcontains $InstallDir) {
            $env:Path = $InstallDir + ';' + $env:Path
        }
    }

    # Load env file into current session (optional keys).
    $envFile = Join-Path $ConfigDir 'env'
    if (Test-Path -LiteralPath $envFile) {
        Get-Content -LiteralPath $envFile | ForEach-Object {
            $line = $_.Trim()
            if ($line -eq '' -or $line.StartsWith('#')) { return }
            if ($line.StartsWith('export ')) { $line = $line.Substring(7).Trim() }
            $eq = $line.IndexOf('=')
            if ($eq -lt 1) { return }
            $k = $line.Substring(0, $eq).Trim()
            $v = $line.Substring($eq + 1).Trim()
            if ($v.Length -ge 2 -and (($v.StartsWith('"') -and $v.EndsWith('"')) -or ($v.StartsWith("'") -and $v.EndsWith("'")))) {
                $v = $v.Substring(1, $v.Length - 2)
            }
            $cur = [Environment]::GetEnvironmentVariable($k, 'Process')
            if ([string]::IsNullOrWhiteSpace($cur)) {
                Set-Item -Path "Env:$k" -Value $v
            }
        }
    }

    Write-Host ""
    Write-Host "Installed YunmengZe Agent $tag"
    Write-Host "  $InstallDir\ymz.exe"
    Write-Host ""
    Write-Host "API key (pick one; nothing is forced):"
    Write-Host "  1) Edit $ConfigDir\env  (e.g. DEEPSEEK1_API_KEY=...)"
    Write-Host "  2) Set user/process env:  `$env:DEEPSEEK1_API_KEY = '...'"
    Write-Host "  3) Literal apiKey string in $ConfigDir\agent.json"
    Write-Host "  4) apiKey `"{file:path}`" relative to config dir"
    Write-Host ""
    Write-Host "Open a new terminal if PATH just updated, then:"
    Write-Host "  ymz"
    Write-Host "  ymz version"
} finally {
    if (Test-Path -LiteralPath $tempDir) {
        Remove-Item -LiteralPath $tempDir -Recurse -Force
    }
}
