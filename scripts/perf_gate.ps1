param(
    [int]$MaxNsPerOp = 50000000,
    [double]$MinOpsPerSec = 20.0,
    [int]$MaxAllocsPerOp = 40000
)

$ErrorActionPreference = "Continue"
$PSNativeCommandUseErrorActionPreference = $false

if (-not $env:GOCACHE) {
    $env:GOCACHE = Join-Path (Get-Location) ".gocache"
}
$env:GOTELEMETRY = "off"

$cmd = "go test ./internal/service -run '^$' -bench '^BenchmarkRunJobColdPath$' -benchmem -count 1"
Write-Host "Running performance gate: $cmd"

$output = & go test ./internal/service -run '^$' -bench '^BenchmarkRunJobColdPath$' -benchmem -count 1 2>&1
$exitCode = $LASTEXITCODE
$text = ($output | Out-String)
Write-Host $text
if ($exitCode -ne 0) {
    throw "perf-gate: benchmark command failed with exit code $exitCode"
}

$line = ($text -split "`r?`n" | Where-Object { $_ -match '^BenchmarkRunJobColdPath\b' } | Select-Object -First 1)
if (-not $line) {
    throw "perf-gate: benchmark line not found for BenchmarkRunJobColdPath"
}

if ($line -notmatch 'BenchmarkRunJobColdPath\S*\s+\d+\s+(\d+)\s+ns/op\s+(\d+)\s+B/op\s+(\d+)\s+allocs/op') {
    throw "perf-gate: unable to parse benchmark metrics: $line"
}

$nsPerOp = [int64]$Matches[1]
$bytesPerOp = [int64]$Matches[2]
$allocsPerOp = [int64]$Matches[3]
$opsPerSec = 1000000000.0 / [double]$nsPerOp

Write-Host ("Parsed metrics: ns/op={0}, B/op={1}, allocs/op={2}, ops/sec={3:n2}" -f $nsPerOp, $bytesPerOp, $allocsPerOp, $opsPerSec)

if ($nsPerOp -gt $MaxNsPerOp) {
    throw ("perf-gate failed: ns/op {0} exceeded max {1}" -f $nsPerOp, $MaxNsPerOp)
}
if ($opsPerSec -lt $MinOpsPerSec) {
    throw ("perf-gate failed: ops/sec {0:n2} below min {1:n2}" -f $opsPerSec, $MinOpsPerSec)
}
if ($allocsPerOp -gt $MaxAllocsPerOp) {
    throw ("perf-gate failed: allocs/op {0} exceeded max {1}" -f $allocsPerOp, $MaxAllocsPerOp)
}

Write-Host "perf-gate passed"
