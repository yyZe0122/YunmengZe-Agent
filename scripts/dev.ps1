[CmdletBinding()]
param(
    [ValidateSet('format', 'lint', 'check', 'build', 'install', 'uninstall', 'all')]
    [string]$Action = 'all',

    [string]$InstallDir = $env:YMZ_INSTALL_DIR
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot

$Go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $Go) {
    $CommonPath = 'C:\Program Files\Go\bin\go.exe'
    if (Test-Path -LiteralPath $CommonPath) {
        $Go = $CommonPath
    }
}
if (-not $Go) {
    $LocalToolchain = Join-Path $Root '.cache/toolchain/go/bin/go.exe'
    if (Test-Path -LiteralPath $LocalToolchain) { $Go = $LocalToolchain }
}
if (-not $Go) {
    throw 'Go was not found in PATH or C:\Program Files\Go\bin\go.exe.'
}

$GoFmt = Join-Path (Split-Path -Parent $Go) 'gofmt.exe'
if (-not (Test-Path -LiteralPath $GoFmt)) {
    $GoFmt = (Get-Command gofmt -ErrorAction SilentlyContinue).Source
}
if (-not $GoFmt) {
    throw 'gofmt was not found next to the selected Go executable or in PATH.'
}

$CacheRoot = Join-Path $Root '.cache'
$env:GOCACHE = Join-Path $CacheRoot 'go-build'
$env:GOMODCACHE = Join-Path $CacheRoot 'gomod'
$env:GOPATH = Join-Path $CacheRoot 'gopath'
$env:GOTELEMETRY = 'off'
$env:CGO_ENABLED = '0'
New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOMODCACHE, $env:GOPATH | Out-Null

$Commands = @('ymz')

function Invoke-Go {
    param([Parameter(Mandatory)][string[]]$Arguments)

    & $Go @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "go $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
    }
}

function Get-GoSourceFiles {
    $excludedDirectories = @('.git', '.cache', 'bin', 'dist', '.crush')
    $sourceRoots = Get-ChildItem -LiteralPath $Root -Directory |
        Where-Object { $_.Name -notin $excludedDirectories }

    $files = @(
        Get-ChildItem -LiteralPath $Root -File -Filter '*.go'
        foreach ($sourceRoot in $sourceRoots) {
            Get-ChildItem -LiteralPath $sourceRoot.FullName -Recurse -File -Filter '*.go'
        }
    )
    return @($files | Sort-Object FullName | ForEach-Object { $_.FullName })
}

function Invoke-GoFmt {
    param([Parameter(Mandatory)][bool]$Write)

    $files = @(Get-GoSourceFiles)
    $unformatted = @()
    $batchSize = 50
    for ($offset = 0; $offset -lt $files.Count; $offset += $batchSize) {
        $last = [Math]::Min($offset + $batchSize - 1, $files.Count - 1)
        $batch = @($files[$offset..$last])
        if ($Write) {
            & $GoFmt -w @batch
        }
        else {
            $unformatted += @(& $GoFmt -l @batch)
        }
        if ($LASTEXITCODE -ne 0) {
            throw "gofmt failed with exit code $LASTEXITCODE"
        }
    }

    if (-not $Write -and $unformatted.Count -gt 0) {
        throw "Go formatting check failed:`n$($unformatted -join [Environment]::NewLine)"
    }
}

function Invoke-Lint {
    Invoke-GoFmt -Write $false
    Invoke-Go -Arguments @('vet', './...')
}

function Invoke-Check {
    Invoke-Lint
    Invoke-Go -Arguments @('test', './...', '-count=1')
}

function Invoke-Build {
    $binDirectory = Join-Path $Root 'bin'
    New-Item -ItemType Directory -Force -Path $binDirectory | Out-Null
    foreach ($command in $Commands) {
        Invoke-Go -Arguments @('build', '-trimpath', '-o', (Join-Path $binDirectory "$command.exe"), "./cmd/$command")
    }
}

function Get-InstallDirectory {
    if (-not [string]::IsNullOrWhiteSpace($InstallDir)) {
        return $InstallDir
    }
    if (-not [string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        return (Join-Path $env:LOCALAPPDATA 'Programs\YunmengZe\bin')
    }
    return (Join-Path $env:USERPROFILE 'YunmengZe\bin')
}

function Invoke-Install {
    Invoke-Build
    $destination = Get-InstallDirectory
    New-Item -ItemType Directory -Force -Path $destination | Out-Null
    $binDirectory = Join-Path $Root 'bin'
    foreach ($name in @('ymz.exe')) {
        Copy-Item -LiteralPath (Join-Path $binDirectory $name) -Destination (Join-Path $destination $name) -Force
    }
    Write-Host "Installed to $destination : ymz"
    $pathEntries = $env:PATH -split ';'
    if ($pathEntries -notcontains $destination) {
        Write-Host "Add to PATH (or open a new terminal after user install.ps1 PATH update):"
        Write-Host "  $destination"
    }
}

function Invoke-Uninstall {
    $destination = Get-InstallDirectory
    foreach ($name in @('ymz.exe')) {
        $path = Join-Path $destination $name
        if (Test-Path -LiteralPath $path) {
            Remove-Item -LiteralPath $path -Force
        }
    }
    Write-Host "Removed from $destination : ymz"
}

Push-Location $Root
try {
    switch ($Action) {
        'format' {
            Invoke-GoFmt -Write $true
        }
        'lint' {
            Invoke-Lint
        }
        'check' {
            Invoke-Check
        }
        'build' {
            Invoke-Build
        }
        'install' {
            Invoke-Install
        }
        'uninstall' {
            Invoke-Uninstall
        }
        'all' {
            Invoke-Check
            Invoke-Build
            & (Join-Path $Root 'bin/ymz.exe') version
            if ($LASTEXITCODE -ne 0) { throw 'ymz version failed' }
        }
    }
}
finally {
    Pop-Location
}
