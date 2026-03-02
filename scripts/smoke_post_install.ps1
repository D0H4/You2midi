param(
    [string]$BinaryPath = "dist/desktop/you2midi-backend.exe",
    [int]$Port = 18080,
    [int]$TimeoutSec = 60,
    [switch]$RunFullTranscription,
    [string]$YoutubeUrl = ""
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $BinaryPath)) {
    throw "Binary not found: $BinaryPath"
}

$tmpRoot = Join-Path $env:TEMP ("you2midi-smoke-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tmpRoot | Out-Null

$configPath = Join-Path $tmpRoot "smoke.toml"
$configToml = @"
[engine]
device              = "auto"
max_attempts        = 2
max_concurrent_jobs = 1
max_concurrent_cpu  = 0
max_concurrent_gpu  = 0
queue_size          = 16

[workspace]
root          = "$($tmpRoot.Replace('\','/'))/workspace"
temp_file_ttl = "24h"
disk_quota_mb = 1024

[server]
host            = "127.0.0.1"
port            = $Port
jwt_secret      = ""
allowed_origins = ["http://localhost:5173"]
"@
[System.IO.File]::WriteAllText($configPath, $configToml, [System.Text.UTF8Encoding]::new($false))

$proc = Start-Process -FilePath (Resolve-Path $BinaryPath) -ArgumentList @("-config", $configPath) -PassThru

try {
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    $healthOk = $false
    while ((Get-Date) -lt $deadline) {
        try {
            $res = Invoke-RestMethod -Method GET -Uri "http://127.0.0.1:$Port/health" -TimeoutSec 3
            if ($res.status -eq "ok") {
                $healthOk = $true
                break
            }
        } catch {
            Start-Sleep -Milliseconds 500
        }
    }

    if (-not $healthOk) {
        throw "Smoke test failed: /health did not become ready within ${TimeoutSec}s"
    }
    Write-Host "Smoke: health endpoint ready"

    if ($RunFullTranscription) {
        if ([string]::IsNullOrWhiteSpace($YoutubeUrl)) {
            throw "RunFullTranscription requires -YoutubeUrl"
        }

        $createBody = @{ youtube_url = $YoutubeUrl; device = "cpu" } | ConvertTo-Json
        $job = Invoke-RestMethod -Method POST -Uri "http://127.0.0.1:$Port/jobs" -ContentType "application/json" -Body $createBody -TimeoutSec 10
        if (-not $job.id) {
            throw "Smoke full run: create job returned no id"
        }

        $jobDeadline = (Get-Date).AddMinutes(6)
        $completed = $false
        while ((Get-Date) -lt $jobDeadline) {
            $state = Invoke-RestMethod -Method GET -Uri "http://127.0.0.1:$Port/jobs/$($job.id)" -TimeoutSec 10
            if ($state.state -eq "completed") {
                $completed = $true
                break
            }
            if ($state.state -eq "failed" -or $state.state -eq "cancelled") {
                throw "Smoke full run failed. Final state: $($state.state), message: $($state.error_message)"
            }
            Start-Sleep -Seconds 2
        }
        if (-not $completed) {
            throw "Smoke full run timed out waiting for completion"
        }

        $midiOut = Join-Path $tmpRoot "smoke.mid"
        Invoke-WebRequest -Method GET -Uri "http://127.0.0.1:$Port/jobs/$($job.id)/midi" -OutFile $midiOut -TimeoutSec 30 | Out-Null
        $midiFile = Get-Item $midiOut
        if ($midiFile.Length -le 0) {
            throw "Smoke full run: downloaded MIDI is empty"
        }
        Write-Host "Smoke: full transcription flow passed"
    }
} finally {
    if ($proc -and -not $proc.HasExited) {
        Stop-Process -Id $proc.Id -Force
    }
    Remove-Item -LiteralPath $tmpRoot -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host "Smoke test passed"
