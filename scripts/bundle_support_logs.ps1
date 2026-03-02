param(
    [string]$OutputDir = "dist/support",
    [string]$ConfigPath = "config.toml",
    [string]$WorkspaceRoot = "",
    [bool]$IncludeWorkspaceDb = $true,
    [bool]$KeepExpanded = $true,
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

function Expand-TildePath {
    param([string]$PathValue)
    if ([string]::IsNullOrWhiteSpace($PathValue)) {
        return $PathValue
    }
    if ($PathValue.StartsWith("~/") -or $PathValue.StartsWith("~\")) {
        return Join-Path $HOME $PathValue.Substring(2)
    }
    return $PathValue
}

function Resolve-WorkspaceRootFromConfig {
    param([string]$Path)
    if (-not (Test-Path $Path)) {
        return $null
    }

    $inWorkspace = $false
    foreach ($line in Get-Content -Path $Path) {
        $trimmed = $line.Trim()
        if ($trimmed -match "^\[workspace\]$") {
            $inWorkspace = $true
            continue
        }
        if ($trimmed -match "^\[.+\]$" -and $trimmed -ne "[workspace]") {
            $inWorkspace = $false
            continue
        }
        if ($inWorkspace -and $trimmed -match '^root\s*=\s*"([^"]+)"') {
            return Expand-TildePath $Matches[1]
        }
    }
    return $null
}

function Copy-OptionalFile {
    param(
        [string]$SourcePath,
        [string]$BundleRoot,
        [string]$RelativeTarget
    )
    if (-not (Test-Path $SourcePath)) {
        return $false
    }
    $target = Join-Path $BundleRoot $RelativeTarget
    $targetDir = Split-Path -Path $target -Parent
    if ($targetDir) {
        New-Item -ItemType Directory -Force -Path $targetDir | Out-Null
    }
    Copy-Item -Path $SourcePath -Destination $target -Force
    return $true
}

function Remove-DirectoryWithRetry {
    param(
        [string]$Path,
        [int]$MaxAttempts = 5
    )
    for ($i = 1; $i -le $MaxAttempts; $i++) {
        if (-not (Test-Path $Path)) {
            return $true
        }
        try {
            Remove-Item -Recurse -Force $Path -ErrorAction Stop
            return $true
        } catch {
            Start-Sleep -Milliseconds (100 * $i)
        }
    }
    return -not (Test-Path $Path)
}

$timestamp = (Get-Date).ToUniversalTime().ToString("yyyyMMdd-HHmmss")
$bundleRoot = Join-Path $OutputDir ("support-bundle-" + $timestamp)
$zipPath = Join-Path $OutputDir ("you2midi-support-" + $timestamp + ".zip")

if (Test-Path $bundleRoot) {
    Remove-Item -Recurse -Force $bundleRoot
}
New-Item -ItemType Directory -Force -Path $bundleRoot | Out-Null

$resolvedConfigPath = $null
if (Test-Path $ConfigPath) {
    $resolvedConfigPath = (Resolve-Path $ConfigPath).Path
}
$resolvedWorkspaceRoot = $WorkspaceRoot
if (-not $resolvedWorkspaceRoot) {
    $resolvedWorkspaceRoot = Resolve-WorkspaceRootFromConfig -Path $ConfigPath
}
if (-not $resolvedWorkspaceRoot) {
    $resolvedWorkspaceRoot = Join-Path $HOME ".you2midi/jobs"
}
$resolvedWorkspaceRoot = [System.IO.Path]::GetFullPath((Expand-TildePath $resolvedWorkspaceRoot))

$copied = @()
foreach ($entry in @(
    @{ src = "config.toml"; dst = "config/config.toml" },
    @{ src = "config.example.toml"; dst = "config/config.example.toml" },
    @{ src = "dist/desktop/artifact-manifest.json"; dst = "artifacts/artifact-manifest.json" },
    @{ src = "dist/security/windows-security-report.json"; dst = "security/windows-security-report.json" },
    @{ src = "docs/third_party_license_inventory.json"; dst = "docs/third_party_license_inventory.json" },
    @{ src = "docs/license-compliance.md"; dst = "docs/license-compliance.md" },
    @{ src = "docs/windows-security-validation.md"; dst = "docs/windows-security-validation.md" }
)) {
    if (Copy-OptionalFile -SourcePath $entry.src -BundleRoot $bundleRoot -RelativeTarget $entry.dst) {
        $copied += $entry.src
    }
}

if ($IncludeWorkspaceDb) {
    $dbPath = Join-Path $resolvedWorkspaceRoot "you2midi.db"
    if (Copy-OptionalFile -SourcePath $dbPath -BundleRoot $bundleRoot -RelativeTarget "workspace/you2midi.db") {
        $copied += $dbPath
    } else {
        Stop-OrWarn "Workspace DB not found at '$dbPath'" | Out-Null
    }
}

$envVars = @{}
Get-ChildItem Env:YOU2MIDI_* -ErrorAction SilentlyContinue | ForEach-Object {
    $envVars[$_.Name] = $_.Value
}
$envVars["PATH"] = $env:PATH
$envVars["COMPUTERNAME"] = $env:COMPUTERNAME
$envVars["USERNAME"] = $env:USERNAME
$envVars["PROCESSOR_ARCHITECTURE"] = $env:PROCESSOR_ARCHITECTURE

$goVersion = $null
try {
    $goVersion = (& go version) -join " "
} catch {
    $goVersion = $null
}

$systemInfo = [ordered]@{
    generated_at_utc = (Get-Date).ToUniversalTime().ToString("o")
    machine = $env:COMPUTERNAME
    user = $env:USERNAME
    os = [System.Environment]::OSVersion.VersionString
    ps_version = $PSVersionTable.PSVersion.ToString()
    go_version = $goVersion
    workspace_root = $resolvedWorkspaceRoot
    config_path = $resolvedConfigPath
    copied_files = $copied
    env = $envVars
}

$diagPath = Join-Path $bundleRoot "diagnostics.json"
$diagJson = $systemInfo | ConvertTo-Json -Depth 8
[System.IO.File]::WriteAllText($diagPath, $diagJson, [System.Text.UTF8Encoding]::new($false))

if (Test-Path $zipPath) {
    Remove-Item -Force $zipPath
}
Compress-Archive -Path (Join-Path $bundleRoot "*") -DestinationPath $zipPath -CompressionLevel Optimal

if (-not $KeepExpanded) {
    if (-not (Remove-DirectoryWithRetry -Path $bundleRoot)) {
        Stop-OrWarn "Failed to remove expanded support bundle directory '$bundleRoot'" | Out-Null
    }
}

Write-Host "Support bundle created: $zipPath"
