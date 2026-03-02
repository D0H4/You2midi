param(
    [string]$SourceDir = "dist/desktop",
    [string]$InstallerScript = "installer/You2Midi.iss",
    [string]$OutDir = "dist/installer",
    [string]$AppVersion = "0.1.0",
    [switch]$RequireCudaRuntime,
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

function Resolve-IsccPath {
    $cmd = Get-Command iscc -ErrorAction SilentlyContinue
    if ($cmd) {
        return $cmd.Source
    }

    $userPath = Join-Path $env:LOCALAPPDATA "Programs\Inno Setup 6\ISCC.exe"
    if (Test-Path $userPath) {
        return $userPath
    }

    return $null
}

$isccPath = Resolve-IsccPath
if (-not $isccPath) {
    if (-not (Stop-OrWarn "Inno Setup compiler (iscc) not found. Install Inno Setup first.")) { exit 0 }
}

$srcPath = Resolve-Path $SourceDir -ErrorAction SilentlyContinue
if (-not $srcPath) {
    if (-not (Stop-OrWarn "Desktop artifact directory '$SourceDir' not found. Run desktop build first.")) { exit 0 }
}

$repoRoot = Resolve-Path "."
$ensureLauncherScript = Join-Path $repoRoot "scripts/ensure_launcher_config.ps1"

$exePath = Join-Path $srcPath "you2midi-desktop.exe"
if (-not (Test-Path $exePath)) {
    if (-not (Stop-OrWarn "Desktop executable '$exePath' not found. Run desktop build first.")) { exit 0 }
}
$backendExePath = Join-Path $srcPath "you2midi-backend.exe"
if (-not (Test-Path $backendExePath)) {
    if (-not (Stop-OrWarn "Backend executable '$backendExePath' not found. Run desktop build first.")) { exit 0 }
}
$updaterExePath = Join-Path $srcPath "you2midi-updater.exe"
if (-not (Test-Path $updaterExePath)) {
    if (-not (Stop-OrWarn "Updater executable '$updaterExePath' not found. Run desktop build first.")) { exit 0 }
}
$launcherExePath = Join-Path $srcPath "You2midi.exe"
if (-not (Test-Path $launcherExePath)) {
    if (-not (Stop-OrWarn "Launcher executable '$launcherExePath' not found. Run desktop build first.")) { exit 0 }
}
$launcherConfigPath = Join-Path $srcPath "launcher-config.json"
if (-not (Test-Path $launcherConfigPath)) {
    Write-Warning "Launcher config '$launcherConfigPath' missing. Generating automatically..."
    & powershell -ExecutionPolicy Bypass -File $ensureLauncherScript -SourceDir $srcPath
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path $launcherConfigPath)) {
        if (-not (Stop-OrWarn "Launcher config generation failed for '$launcherConfigPath'.")) { exit 0 }
    }
}
$packagedConfigPath = Join-Path $srcPath "config.toml"
if (-not (Test-Path $packagedConfigPath)) {
    if (-not (Stop-OrWarn "Packaged config '$packagedConfigPath' not found. Run desktop build first.")) { exit 0 }
}

$venvScriptsDir = Join-Path $srcPath "runtime/venv/Scripts"
$runtimeManifestPath = Join-Path $srcPath "runtime/python-runtime.json"
$runtimePythonDir = Join-Path $srcPath "runtime/python"
$hasBundledVenv = Test-Path (Join-Path $venvScriptsDir "python.exe")
$hasRemoteRuntimeManifest = Test-Path $runtimeManifestPath
if (-not $hasBundledVenv -and -not $hasRemoteRuntimeManifest) {
    if (-not (Stop-OrWarn "No Python runtime found. Expected either '$venvScriptsDir\\python.exe' or '$runtimeManifestPath'.")) { exit 0 }
}
if ($hasRemoteRuntimeManifest -and (Test-Path $runtimePythonDir)) {
    Write-Warning "Remote runtime manifest mode detected with stale '$runtimePythonDir'. Installer script excludes runtime/python for remote mode."
}
if ($hasBundledVenv) {
    foreach ($runtimeExe in @("python.exe", "transkun.exe", "yt-dlp.exe")) {
        $runtimePath = Join-Path $venvScriptsDir $runtimeExe
        if (-not (Test-Path $runtimePath)) {
            if (-not (Stop-OrWarn "Bundled runtime '$runtimePath' not found. Re-run desktop build with runtime bundling.")) { exit 0 }
        }
    }
}

$ffmpegPath = Join-Path $srcPath "runtime/ffmpeg/bin/ffmpeg.exe"
if (-not (Test-Path $ffmpegPath)) {
    if (-not (Stop-OrWarn "Bundled runtime '$ffmpegPath' not found. Re-run desktop build with runtime bundling.")) { exit 0 }
}

$webView2Bootstrapper = Join-Path $srcPath "runtime/webview2/MicrosoftEdgeWebView2Setup.exe"
if (-not (Test-Path $webView2Bootstrapper)) {
    if (-not (Stop-OrWarn "WebView2 bootstrapper '$webView2Bootstrapper' not found. Re-run desktop build without -SkipWebView2Bootstrapper.")) { exit 0 }
}

$vcRedistBootstrapper = Join-Path $srcPath "runtime/vcredist/vc_redist.x64.exe"
if (-not (Test-Path $vcRedistBootstrapper)) {
    if (-not (Stop-OrWarn "VC++ Redistributable bootstrapper '$vcRedistBootstrapper' not found. Re-run desktop build without -SkipVCRedistBootstrapper.")) { exit 0 }
}

if ($RequireCudaRuntime) {
    Write-Warning "-RequireCudaRuntime is deprecated and ignored. Standalone CUDA runtime bundling was removed; CUDA support relies on venv-packaged torch libraries and host NVIDIA driver compatibility."
}

if (-not (Test-Path $InstallerScript)) {
    throw "Installer script '$InstallerScript' not found."
}

function Remove-StaleRemoteRuntime {
    param([string]$RootPath)
    foreach ($rel in @("runtime/python", "runtime/python.new", "runtime/python.old")) {
        $target = Join-Path $RootPath $rel
        if (Test-Path $target) {
            Remove-Item -Path $target -Recurse -Force -ErrorAction SilentlyContinue
            if (Test-Path $target) {
                throw "Failed to remove stale remote runtime payload from staged source: $target"
            }
            Write-Host "Removed remote runtime payload from staged source: $target"
        }
    }
}

$effectiveSourceDir = $srcPath
$stageDir = $null
if ($hasRemoteRuntimeManifest) {
    $stageDir = Join-Path $env:TEMP ("you2midi-installer-stage-" + [Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $stageDir | Out-Null
    Write-Host "Staging installer source for remote runtime mode..."
    Copy-Item -Path (Join-Path $srcPath "*") -Destination $stageDir -Recurse -Force
    Remove-StaleRemoteRuntime -RootPath $stageDir
    $effectiveSourceDir = (Resolve-Path $stageDir).Path
}

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$outPath = Resolve-Path $OutDir

$args = @(
    "/DSourceDir=$effectiveSourceDir",
    "/DMyAppVersion=$AppVersion",
    "/O$outPath",
    $InstallerScript
)

# Optional code-signing hook: provide full signtool command via env var.
# Example:
#   $env:INNO_SIGNTOOL = "signtool.exe sign /fd sha256 /tr http://timestamp.digicert.com /td sha256 /a /f C:\cert.pfx /p secret `$f"
if ($env:INNO_SIGNTOOL) {
    $args = @("/DSignToolName=signtool", "/Ssigntool=$($env:INNO_SIGNTOOL)") + $args
}

Write-Host "Building installer with Inno Setup..."
try {
    & $isccPath @args
    if ($LASTEXITCODE -ne 0) {
        throw "iscc failed with exit code $LASTEXITCODE"
    }
} finally {
    if ($stageDir -and (Test-Path $stageDir)) {
        Remove-Item -Path $stageDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Write-Host "Installer build complete. Output directory: $outPath"
