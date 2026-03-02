param(
    [string]$SourceDir = "dist/desktop",
    [string]$GitHubRepo = "",
    [string]$GitHubAPIBase = "https://api.github.com",
    [string]$InstallerAssetPattern = "^You2Midi-Setup-.*\.exe$",
    [string]$PatchAssetPattern = "^you2midi-patch-.*\.zip$",
    [string]$AppExecutable = "you2midi-desktop.exe",
    [string]$UpdaterExecutable = "you2midi-updater.exe",
    [string[]]$InstallDirCandidates = @(
        "%ProgramFiles%\You2Midi",
        "%LOCALAPPDATA%\Programs\You2Midi"
    ),
    [switch]$Force
)

$ErrorActionPreference = "Stop"

function Resolve-GitHubRepoFromGitRemote {
    try {
        $remote = (& git config --get remote.origin.url 2>$null | Select-Object -First 1).Trim()
    } catch {
        return ""
    }
    if (-not $remote) {
        return ""
    }

    if ($remote -match "github\.com[:/](?<owner>[^/]+)/(?<repo>[^/.]+)(\.git)?$") {
        return "$($Matches.owner)/$($Matches.repo)"
    }
    return ""
}

$resolvedSource = Resolve-Path $SourceDir -ErrorAction SilentlyContinue
if (-not $resolvedSource) {
    throw "Source directory '$SourceDir' not found."
}

$configPath = Join-Path $resolvedSource "launcher-config.json"
if ((Test-Path $configPath) -and -not $Force) {
    Write-Host "Launcher config already present: $configPath"
    exit 0
}

$repo = $GitHubRepo
if (-not $repo) {
    $repo = $env:YOU2MIDI_GITHUB_REPO
}
if (-not $repo) {
    $repo = Resolve-GitHubRepoFromGitRemote
}

$config = [ordered]@{
    github_repo = $repo
    github_api_base = $GitHubAPIBase
    installer_asset_pattern = $InstallerAssetPattern
    patch_asset_pattern = $PatchAssetPattern
    app_executable = $AppExecutable
    updater_executable = $UpdaterExecutable
    install_dir_candidates = $InstallDirCandidates
    request_timeout_seconds = 30
}

$json = $config | ConvertTo-Json -Depth 8
[System.IO.File]::WriteAllText($configPath, $json, [System.Text.UTF8Encoding]::new($false))

if (-not $repo) {
    Write-Warning "launcher-config.json generated but github_repo is empty. Set YOU2MIDI_GITHUB_REPO or pass -GitHubRepo."
} else {
    Write-Host "Launcher config generated: $configPath (repo=$repo)"
}
