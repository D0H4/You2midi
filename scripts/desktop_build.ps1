param(
    [string]$ProjectDir = "desktop",
    [string]$OutputDir = "dist/desktop",
    [string]$VenvDir = ".venv",
    [string]$LauncherGitHubRepo = "",
    [string]$LauncherGitHubAPIBase = "https://api.github.com",
    [string]$LauncherInstallerAssetPattern = "^You2Midi-Setup-.*\.exe$",
    [string]$LauncherPatchAssetPattern = "^you2midi-patch-.*\.zip$",
    [string[]]$LauncherInstallDirCandidates = @(
        "%ProgramFiles%\You2Midi",
        "%LOCALAPPDATA%\Programs\You2Midi"
    ),
    [bool]$UseRemotePythonRuntime = $true,
    [string]$PythonRuntimeArchiveUrl = "",
    [string]$PythonRuntimeArchiveSHA256 = "",
    [string]$PythonRuntimeArchiveType = "",
    [string]$PythonRuntimeScriptsRelPath = "Scripts",
    [string]$FfmpegDir = "",
    [string]$CudaRuntimeDir = "",
    [string]$WebView2BootstrapperUrl = "https://go.microsoft.com/fwlink/p/?LinkId=2124703",
    [string]$VCRedistX64Url = "https://aka.ms/vs/17/release/vc_redist.x64.exe",
    [switch]$SkipWebView2Bootstrapper,
    [switch]$SkipVCRedistBootstrapper,
    [switch]$Strict
)

$ErrorActionPreference = "Stop"

function Stop-OrWarn {
    param([string]$Message)
    if ($Strict) {
        throw $Message
    }
    Write-Warning $Message
    return $false
}

function Stop-You2MidiRuntimeProcesses {
    foreach ($name in @("You2midi", "you2midi-desktop", "you2midi-backend", "python", "pip")) {
        Stop-Process -Name $name -Force -ErrorAction SilentlyContinue
    }
}

function Resolve-VenvRoot {
    param([string]$InputPath)
    if (-not $InputPath) {
        return $null
    }
    $resolved = Resolve-Path $InputPath -ErrorAction SilentlyContinue
    if (-not $resolved) {
        return $null
    }
    $candidate = $resolved.Path
    if (Test-Path (Join-Path $candidate "pyvenv.cfg")) {
        return $candidate
    }
    return $null
}

function Resolve-FfmpegBinDir {
    param([string]$InputPath)
    if ($InputPath) {
        $resolved = Resolve-Path $InputPath -ErrorAction SilentlyContinue
        if ($resolved) {
            $candidate = $resolved.Path
            if (Test-Path (Join-Path $candidate "ffmpeg.exe")) {
                return $candidate
            }
            $binCandidate = Join-Path $candidate "bin"
            if (Test-Path (Join-Path $binCandidate "ffmpeg.exe")) {
                return $binCandidate
            }
        }
    }

    $ffmpegCmd = Get-Command ffmpeg -ErrorAction SilentlyContinue
    if ($ffmpegCmd -and $ffmpegCmd.Source) {
        return Split-Path -Path $ffmpegCmd.Source -Parent
    }
    return $null
}

function Ensure-WebView2Bootstrapper {
    param(
        [string]$OutputRoot,
        [string]$BootstrapperUrl
    )
    $destDir = Join-Path $OutputRoot "runtime/webview2"
    New-Item -ItemType Directory -Force -Path $destDir | Out-Null
    $destFile = Join-Path $destDir "MicrosoftEdgeWebView2Setup.exe"

    if (Test-Path $destFile) {
        Write-Host "WebView2 bootstrapper already present: $destFile"
        return $destFile
    }

    Write-Host "Downloading WebView2 bootstrapper from '$BootstrapperUrl'..."
    Invoke-WebRequest -Uri $BootstrapperUrl -OutFile $destFile
    if (-not (Test-Path $destFile)) {
        throw "Failed to download WebView2 bootstrapper to '$destFile'"
    }
    return $destFile
}

function Ensure-VCRedistBootstrapper {
    param(
        [string]$OutputRoot,
        [string]$BootstrapperUrl
    )
    $destDir = Join-Path $OutputRoot "runtime/vcredist"
    New-Item -ItemType Directory -Force -Path $destDir | Out-Null
    $destFile = Join-Path $destDir "vc_redist.x64.exe"

    if (Test-Path $destFile) {
        Write-Host "VC++ Redistributable bootstrapper already present: $destFile"
        return $destFile
    }

    Write-Host "Downloading VC++ Redistributable bootstrapper from '$BootstrapperUrl'..."
    Invoke-WebRequest -Uri $BootstrapperUrl -OutFile $destFile
    if (-not (Test-Path $destFile)) {
        throw "Failed to download VC++ Redistributable bootstrapper to '$destFile'"
    }
    return $destFile
}

if (-not (Get-Command wails -ErrorAction SilentlyContinue)) {
    if (-not (Stop-OrWarn "wails CLI not found. Install with: go install github.com/wailsapp/wails/v2/cmd/wails@latest")) { exit 0 }
}

function Resolve-ArchiveType {
    param([string]$ArchiveUrl, [string]$ExplicitType)
    if ($ExplicitType) {
        return $ExplicitType
    }
    $u = $ArchiveUrl.ToLowerInvariant()
    if ($u.EndsWith(".tar.gz") -or $u.EndsWith(".tgz")) {
        return "tar.gz"
    }
    if ($u.EndsWith(".zip")) {
        return "zip"
    }
    return "auto"
}

function Write-PackagedConfig {
    param([string]$OutputRoot)
    $configPath = Join-Path $OutputRoot "config.toml"
    $configText = @'
# You2Midi desktop packaged configuration
# This file is generated during desktop build.
schema_version = 1

[engine]
  device              = "auto"  # auto | cpu | cuda
  max_attempts        = 3
  max_concurrent_jobs = 2
  max_concurrent_cpu  = 1
  max_concurrent_gpu  = 1
  queue_size          = 128
'@
    [System.IO.File]::WriteAllText($configPath, $configText, [System.Text.UTF8Encoding]::new($false))
    Write-Host "Wrote packaged config: $configPath"
}

$repoRoot = Resolve-Path "."
if (-not $env:GOCACHE) {
    $env:GOCACHE = Join-Path $repoRoot ".gocache"
}
if (-not $env:GOMODCACHE) {
    $env:GOMODCACHE = Join-Path $repoRoot ".gomodcache"
}
if (-not $env:GOPATH) {
    $env:GOPATH = Join-Path $repoRoot ".gopath"
}
if (-not $env:GOENV) {
    $env:GOENV = Join-Path $repoRoot ".goenv"
}
foreach ($dir in @($env:GOCACHE, $env:GOMODCACHE, $env:GOPATH)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
}

foreach ($proxyVar in @("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy")) {
    $value = [Environment]::GetEnvironmentVariable($proxyVar)
    if ($value -and $value -match "127\.0\.0\.1:9") {
        Write-Warning "Ignoring invalid proxy in $proxyVar=$value"
        Remove-Item -Path ("Env:" + $proxyVar) -ErrorAction SilentlyContinue
    }
}
$env:GOTELEMETRY = "off"

$projectPath = Resolve-Path $ProjectDir -ErrorAction SilentlyContinue
if (-not $projectPath) {
    if (-not (Stop-OrWarn "Wails project directory '$ProjectDir' not found. Skipping desktop build.")) { exit 0 }
}

$wailsConfigPath = Join-Path $projectPath "wails.json"
if (-not (Test-Path $wailsConfigPath)) {
    if (-not (Stop-OrWarn "wails.json not found in '$ProjectDir'. Initialize Wails project first.")) { exit 0 }
}

New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null
$resolvedOutputDir = Resolve-Path $OutputDir
$backendExePath = Join-Path $resolvedOutputDir "you2midi-backend.exe"
$updaterExePath = Join-Path $resolvedOutputDir "you2midi-updater.exe"
$launcherExePath = Join-Path $resolvedOutputDir "You2midi.exe"
$legacyLauncherExePath = Join-Path $resolvedOutputDir "you2midi-launcher.exe"
if (Test-Path $legacyLauncherExePath) {
    Remove-Item -Path $legacyLauncherExePath -Force
}
$cudaLegacyDir = Join-Path $resolvedOutputDir "runtime/cuda"
if (Test-Path $cudaLegacyDir) {
    Remove-Item -Path $cudaLegacyDir -Recurse -Force
}
$runtimeRootDir = Join-Path $resolvedOutputDir "runtime"
New-Item -ItemType Directory -Force -Path $runtimeRootDir | Out-Null
$runtimeManifestPath = Join-Path $runtimeRootDir "python-runtime.json"

Write-Host "Building backend service executable..."
& go build -o $backendExePath .
if ($LASTEXITCODE -ne 0) {
    throw "go build failed with exit code $LASTEXITCODE"
}

Write-Host "Building updater executable..."
& go build -o $updaterExePath ./cmd/updater
if ($LASTEXITCODE -ne 0) {
    throw "updater go build failed with exit code $LASTEXITCODE"
}

Write-Host "Building launcher executable..."
& go build -o $launcherExePath ./cmd/launcher
if ($LASTEXITCODE -ne 0) {
    throw "launcher go build failed with exit code $LASTEXITCODE"
}

$launcherConfigPath = Join-Path $resolvedOutputDir "launcher-config.json"
$ensureLauncherScript = Join-Path $repoRoot "scripts/ensure_launcher_config.ps1"
$ensureLauncherArgs = @(
    "-ExecutionPolicy", "Bypass",
    "-File", $ensureLauncherScript,
    "-SourceDir", $resolvedOutputDir,
    "-GitHubAPIBase", $LauncherGitHubAPIBase,
    "-InstallerAssetPattern", $LauncherInstallerAssetPattern,
    "-PatchAssetPattern", $LauncherPatchAssetPattern,
    "-AppExecutable", "you2midi-desktop.exe",
    "-UpdaterExecutable", "you2midi-updater.exe",
    "-Force"
)
if ($LauncherGitHubRepo) {
    $ensureLauncherArgs += @("-GitHubRepo", $LauncherGitHubRepo)
}
& powershell @ensureLauncherArgs
if ($LASTEXITCODE -ne 0) {
    throw "launcher config generation failed with exit code $LASTEXITCODE"
}

Write-PackagedConfig -OutputRoot $resolvedOutputDir

if ($UseRemotePythonRuntime) {
    Stop-You2MidiRuntimeProcesses

    $archiveUrl = $PythonRuntimeArchiveUrl
    if (-not $archiveUrl) {
        $archiveUrl = $env:YOU2MIDI_PYTHON_RUNTIME_URL
    }
    if (-not $archiveUrl) {
        if (-not (Stop-OrWarn "Remote Python runtime mode enabled, but runtime archive URL not provided. Set -PythonRuntimeArchiveUrl or YOU2MIDI_PYTHON_RUNTIME_URL.")) { exit 0 }
    }

    $manifest = [ordered]@{
        version = (Get-Date).ToUniversalTime().ToString("yyyyMMdd-HHmmss")
        archive_url = $archiveUrl
        archive_sha256 = $PythonRuntimeArchiveSHA256
        scripts_rel_path = $PythonRuntimeScriptsRelPath
        archive_type = (Resolve-ArchiveType -ArchiveUrl $archiveUrl -ExplicitType $PythonRuntimeArchiveType)
    }
    $manifestJson = $manifest | ConvertTo-Json -Depth 8
    [System.IO.File]::WriteAllText($runtimeManifestPath, $manifestJson, [System.Text.UTF8Encoding]::new($false))
    Write-Host "Wrote remote runtime manifest: $runtimeManifestPath"

    # Remote runtime mode ships only manifest in installer.
    # Do not force-delete local runtime/python here: with file locks this can partially delete
    # the runtime and cause first-run bootstrap errors (e.g., missing encodings).
    $staleRuntimePaths = @(
        (Join-Path $runtimeRootDir "python"),
        (Join-Path $runtimeRootDir "python.new"),
        (Join-Path $runtimeRootDir "python.old"),
        (Join-Path $runtimeRootDir ".deps_ready.json")
    )
    $existingStaleRuntime = @($staleRuntimePaths | Where-Object { Test-Path $_ })
    if ($existingStaleRuntime.Count -gt 0) {
        Write-Warning "Existing local runtime payload detected (left as-is to avoid partial deletion under file locks). Installer staging excludes runtime/python automatically."
    }

    $venvDestDir = Join-Path $runtimeRootDir "venv"
    if (Test-Path $venvDestDir) {
        Remove-Item -Path $venvDestDir -Recurse -Force
    }
} else {
    $venvSource = Resolve-VenvRoot -InputPath $VenvDir
    if (-not $venvSource) {
        if (-not (Stop-OrWarn "Python venv '$VenvDir' not found (pyvenv.cfg missing).")) { exit 0 }
    }
    $venvScripts = Join-Path $venvSource "Scripts"
    foreach ($required in @("python.exe", "transkun.exe", "yt-dlp.exe")) {
        $requiredPath = Join-Path $venvScripts $required
        if (-not (Test-Path $requiredPath)) {
            if (-not (Stop-OrWarn "Required runtime binary '$requiredPath' not found.")) { exit 0 }
        }
    }
    $venvDestDir = Join-Path $runtimeRootDir "venv"
    if (Test-Path $venvDestDir) {
        Remove-Item -Path $venvDestDir -Recurse -Force
    }
    if (Test-Path $runtimeManifestPath) {
        Remove-Item -Path $runtimeManifestPath -Force
    }
    New-Item -ItemType Directory -Force -Path (Split-Path $venvDestDir -Parent) | Out-Null
    Write-Host "Bundling Python venv from '$venvSource'..."
    Copy-Item -Path $venvSource -Destination $venvDestDir -Recurse -Force
}

$ffmpegSource = $FfmpegDir
if (-not $ffmpegSource) {
    $ffmpegSource = $env:YOU2MIDI_FFMPEG_DIR
}
$ffmpegBinDir = Resolve-FfmpegBinDir -InputPath $ffmpegSource
if (-not $ffmpegBinDir) {
    if (-not (Stop-OrWarn "ffmpeg binary directory not found. Set -FfmpegDir or YOU2MIDI_FFMPEG_DIR.")) { exit 0 }
}
$ffmpegDestDir = Join-Path $resolvedOutputDir "runtime/ffmpeg/bin"
New-Item -ItemType Directory -Force -Path $ffmpegDestDir | Out-Null
Write-Host "Bundling ffmpeg binaries from '$ffmpegBinDir'..."
Copy-Item -Path (Join-Path $ffmpegBinDir "ff*.exe") -Destination $ffmpegDestDir -Force
if (-not (Test-Path (Join-Path $ffmpegDestDir "ffmpeg.exe"))) {
    if (-not (Stop-OrWarn "Bundled ffmpeg.exe not found after copy.")) { exit 0 }
}

if (-not $SkipWebView2Bootstrapper) {
    try {
        $wv2Bootstrapper = Ensure-WebView2Bootstrapper -OutputRoot $resolvedOutputDir -BootstrapperUrl $WebView2BootstrapperUrl
        Write-Host "Bundled WebView2 bootstrapper: $wv2Bootstrapper"
    } catch {
        if (-not (Stop-OrWarn "WebView2 bootstrapper bundle failed: $($_.Exception.Message)")) { exit 0 }
    }
}

if (-not $SkipVCRedistBootstrapper) {
    try {
        $vcRedistBootstrapper = Ensure-VCRedistBootstrapper -OutputRoot $resolvedOutputDir -BootstrapperUrl $VCRedistX64Url
        Write-Host "Bundled VC++ Redistributable bootstrapper: $vcRedistBootstrapper"
    } catch {
        if (-not (Stop-OrWarn "VC++ Redistributable bootstrapper bundle failed: $($_.Exception.Message)")) { exit 0 }
    }
}

if ($CudaRuntimeDir -or $env:YOU2MIDI_CUDA_RUNTIME_DIR -or $env:CUDA_PATH) {
    Write-Host "Skipping standalone CUDA runtime bundling (runtime/cuda). Using CUDA libraries bundled in the Python venv (torch wheel)."
}

Write-Host "Building desktop app with Wails..."
Push-Location $projectPath
try {
    & wails build -clean -platform windows/amd64
    if ($LASTEXITCODE -ne 0) {
        throw "wails build failed with exit code $LASTEXITCODE"
    }
} finally {
    Pop-Location
}

$binDir = Join-Path $projectPath "build/bin"
if (-not (Test-Path $binDir)) {
    throw "wails build completed but '$binDir' does not exist."
}

$exe = Get-ChildItem -Path $binDir -Filter *.exe -File | Select-Object -First 1
if (-not $exe) {
    throw "No desktop executable found under '$binDir'."
}

$destExe = Join-Path $resolvedOutputDir "you2midi-desktop.exe"
Copy-Item -Path $exe.FullName -Destination $destExe -Force
Copy-Item -Path $backendExePath -Destination (Join-Path $binDir "you2midi-backend.exe") -Force
Copy-Item -Path $updaterExePath -Destination (Join-Path $binDir "you2midi-updater.exe") -Force
Copy-Item -Path $launcherExePath -Destination (Join-Path $binDir "You2midi.exe") -Force
Copy-Item -Path $launcherConfigPath -Destination (Join-Path $binDir "launcher-config.json") -Force

Write-Host "Desktop executable ready: $destExe"
Write-Host "Backend executable ready: $backendExePath"
Write-Host "Updater executable ready: $updaterExePath"
Write-Host "Launcher executable ready: $launcherExePath"
