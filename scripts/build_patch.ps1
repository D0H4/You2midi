param(
    [string]$SourceDir = "dist/desktop",
    [string]$OutputDir = "dist/patch",
    [string]$Version = "dev",
    [string]$LaunchExecutable = "you2midi-desktop.exe",
    [string[]]$IncludeFiles = @(
        "You2midi.exe",
        "launcher-config.json",
        "you2midi-desktop.exe",
        "you2midi-backend.exe",
        "you2midi-updater.exe",
        "runtime/python-runtime.json"
    )
)

$ErrorActionPreference = "Stop"

$resolvedSource = Resolve-Path $SourceDir -ErrorAction SilentlyContinue
if (-not $resolvedSource) {
    throw "Patch source directory '$SourceDir' not found."
}

$launcherExe = Join-Path $resolvedSource "You2midi.exe"
$launcherConfig = Join-Path $resolvedSource "launcher-config.json"
if ((Test-Path $launcherExe) -and -not (Test-Path $launcherConfig)) {
    $repoRoot = Resolve-Path "."
    $ensureLauncherScript = Join-Path $repoRoot "scripts/ensure_launcher_config.ps1"
    Write-Warning "launcher-config.json missing. Generating automatically for patch source..."
    & powershell -ExecutionPolicy Bypass -File $ensureLauncherScript -SourceDir $resolvedSource
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to generate launcher-config.json for patch source."
    }
}

if (-not $IncludeFiles -or $IncludeFiles.Count -eq 0) {
    throw "IncludeFiles must contain at least one file."
}

New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null
$resolvedOutputDir = (Resolve-Path $OutputDir).Path

$stageDir = Join-Path $env:TEMP ("you2midi-patch-stage-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $stageDir | Out-Null

try {
    $manifestFiles = @()

    foreach ($relativePath in $IncludeFiles) {
        $cleanRelative = $relativePath.Trim()
        if ([string]::IsNullOrWhiteSpace($cleanRelative)) {
            continue
        }
        $sourcePath = Join-Path $resolvedSource $cleanRelative
        if (-not (Test-Path $sourcePath)) {
            # Allow optional files in default include list.
            Write-Host "Skipping missing optional patch file: $cleanRelative"
            continue
        }
        $sourceItem = Get-Item -LiteralPath $sourcePath
        if ($sourceItem.PSIsContainer) {
            throw "Included path '$cleanRelative' is a directory. Include files only."
        }

        $destPath = Join-Path $stageDir $cleanRelative
        $destDir = Split-Path -Path $destPath -Parent
        if ($destDir) {
            New-Item -ItemType Directory -Force -Path $destDir | Out-Null
        }
        Copy-Item -LiteralPath $sourcePath -Destination $destPath -Force

        $hash = (Get-FileHash -LiteralPath $destPath -Algorithm SHA256).Hash.ToLowerInvariant()
        $manifestFiles += [ordered]@{
            path = $cleanRelative.Replace('\', '/')
            sha256 = $hash
            size_bytes = [int64]$sourceItem.Length
        }
    }

    if ($manifestFiles.Count -eq 0) {
        throw "No valid files were included in patch."
    }

    $manifest = [ordered]@{
        version = $Version
        created_at_utc = (Get-Date).ToUniversalTime().ToString("o")
        launch_executable = $LaunchExecutable
        files = $manifestFiles
    }
    $manifestPath = Join-Path $stageDir "patch-manifest.json"
    $manifestJson = $manifest | ConvertTo-Json -Depth 8
    [System.IO.File]::WriteAllText($manifestPath, $manifestJson, [System.Text.UTF8Encoding]::new($false))

    $safeVersion = ($Version -replace '[^A-Za-z0-9._-]', '_')
    $zipPath = Join-Path $resolvedOutputDir ("you2midi-patch-" + $safeVersion + ".zip")
    if (Test-Path $zipPath) {
        Remove-Item -LiteralPath $zipPath -Force
    }

    Compress-Archive -Path (Join-Path $stageDir "*") -DestinationPath $zipPath -CompressionLevel Optimal
    Write-Host "Patch package created: $zipPath"
} finally {
    Remove-Item -LiteralPath $stageDir -Recurse -Force -ErrorAction SilentlyContinue
}
