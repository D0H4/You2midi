param(
    [string]$InputRoot = "dist/desktop",
    [string]$OutputPath = "dist/desktop/artifact-manifest.json"
)

$ErrorActionPreference = "Stop"

$root = Resolve-Path $InputRoot -ErrorAction SilentlyContinue
if (-not $root) {
    throw "Input root '$InputRoot' does not exist."
}

function Try-GetVersion {
    param(
        [string]$BinaryPath,
        [string[]]$Args = @("--version")
    )

    if (-not (Test-Path $BinaryPath)) {
        return $null
    }
    try {
        $out = & $BinaryPath @Args 2>&1 | Out-String
        return ($out -split "`r?`n" | Select-Object -First 1).Trim()
    } catch {
        return $null
    }
}

$files = Get-ChildItem -Path $root -Recurse -File
$entries = @()
foreach ($file in $files) {
    $hash = Get-FileHash -Path $file.FullName -Algorithm SHA256
    $relative = $file.FullName.Substring($root.Path.Length).TrimStart('\', '/')
    $entries += [ordered]@{
        path = $relative.Replace('\', '/')
        size_bytes = [int64]$file.Length
        sha256 = $hash.Hash.ToLowerInvariant()
    }
}

$runtime = [ordered]@{
    python = Try-GetVersion -BinaryPath (Join-Path $root "runtime/venv/Scripts/python.exe")
    transkun = Try-GetVersion -BinaryPath (Join-Path $root "runtime/venv/Scripts/transkun.exe")
    ytdlp = Try-GetVersion -BinaryPath (Join-Path $root "runtime/venv/Scripts/yt-dlp.exe")
    ffmpeg = Try-GetVersion -BinaryPath (Join-Path $root "runtime/ffmpeg/bin/ffmpeg.exe")
    neuralnote = Try-GetVersion -BinaryPath (Join-Path $root "runtime/neuralnote/neuralnote.exe")
}

$manifest = [ordered]@{
    generated_at_utc = (Get-Date).ToUniversalTime().ToString("o")
    root = $root.Path
    file_count = $entries.Count
    runtime_versions = $runtime
    artifacts = $entries
}

$outDir = Split-Path -Path $OutputPath -Parent
if ($outDir) {
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
}
$json = $manifest | ConvertTo-Json -Depth 8
$fullOutputPath = [System.IO.Path]::GetFullPath($OutputPath)
[System.IO.File]::WriteAllText($fullOutputPath, $json, [System.Text.UTF8Encoding]::new($false))

Write-Host "Artifact manifest written: $OutputPath"
