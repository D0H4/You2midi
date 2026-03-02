param(
    [string]$OutputDir = "dist/desktop/runtime",
    [string]$Version = "",
    [Parameter(Mandatory = $true)]
    [string]$ArchiveUrl,
    [string]$ArchiveSHA256 = "",
    [string]$ScriptsRelPath = "Scripts",
    [string]$ArchiveType = ""
)

$ErrorActionPreference = "Stop"

if (-not $Version) {
    $Version = (Get-Date).ToUniversalTime().ToString("yyyyMMdd-HHmmss")
}
if (-not $ArchiveType) {
    $urlLower = $ArchiveUrl.ToLowerInvariant()
    if ($urlLower.EndsWith(".tar.gz") -or $urlLower.EndsWith(".tgz")) {
        $ArchiveType = "tar.gz"
    } elseif ($urlLower.EndsWith(".zip")) {
        $ArchiveType = "zip"
    } else {
        $ArchiveType = "auto"
    }
}

New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null
$manifestPath = Join-Path (Resolve-Path $OutputDir).Path "python-runtime.json"

$manifest = [ordered]@{
    version = $Version
    archive_url = $ArchiveUrl
    archive_sha256 = $ArchiveSHA256
    scripts_rel_path = $ScriptsRelPath
    archive_type = $ArchiveType
}

$json = $manifest | ConvertTo-Json -Depth 8
[System.IO.File]::WriteAllText($manifestPath, $json, [System.Text.UTF8Encoding]::new($false))

Write-Host "Python runtime manifest written: $manifestPath"
