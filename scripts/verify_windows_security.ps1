param(
    [string]$InstallerPath = "",
    [string]$DesktopExePath = "",
    [string]$OutputPath = "dist/security/windows-security-report.json",
    [string]$ExpectedPublisher = "",
    [switch]$RequireSignature,
    [bool]$RunDefenderScan = $true,
    [switch]$Strict
)

$ErrorActionPreference = "Stop"

function Resolve-ArtifactPath {
    param(
        [string]$Path,
        [string]$FallbackGlob
    )

    if ($Path -and (Test-Path $Path)) {
        return (Resolve-Path $Path).Path
    }

    $fallback = Get-ChildItem -Path $FallbackGlob -File -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTimeUtc -Descending |
        Select-Object -First 1
    if ($fallback) {
        return $fallback.FullName
    }
    return $null
}

function Get-DefenderCmd {
    $candidates = @(
        (Join-Path $env:ProgramFiles "Windows Defender\MpCmdRun.exe"),
        "C:\Program Files\Windows Defender\MpCmdRun.exe"
    )
    foreach ($candidate in $candidates) {
        if (Test-Path $candidate) {
            return $candidate
        }
    }
    return $null
}

function New-FileCheck {
    param(
        [string]$Path,
        [string]$ExpectedPublisher,
        [bool]$RequireSignature,
        [bool]$RunDefenderScan
    )

    if (-not (Test-Path $Path)) {
        return [ordered]@{
            path = $Path
            present = $false
            errors = @("file not found")
        }
    }

    $sig = Get-AuthenticodeSignature -FilePath $Path
    $hash = Get-FileHash -Path $Path -Algorithm SHA256
    $file = Get-Item -Path $Path

    $signerSubject = $null
    $signerThumbprint = $null
    if ($sig.SignerCertificate) {
        $signerSubject = $sig.SignerCertificate.Subject
        $signerThumbprint = $sig.SignerCertificate.Thumbprint
    }

    $timestampSubject = $null
    if ($sig.TimeStamperCertificate) {
        $timestampSubject = $sig.TimeStamperCertificate.Subject
    }

    $errors = @()
    if ($RequireSignature -and $sig.Status -ne "Valid") {
        $errors += "signature status is '$($sig.Status)'"
    }
    if ($RequireSignature -and -not $sig.TimeStamperCertificate) {
        $errors += "missing timestamp certificate"
    }
    if ($ExpectedPublisher -and $sig.SignerCertificate) {
        if ($sig.SignerCertificate.Subject -notlike "*$ExpectedPublisher*") {
            $errors += "signer subject mismatch (expected to contain '$ExpectedPublisher')"
        }
    } elseif ($ExpectedPublisher -and -not $sig.SignerCertificate) {
        $errors += "expected publisher '$ExpectedPublisher' but no signer certificate is present"
    }

    $defender = [ordered]@{
        executed = $false
        exit_code = $null
        cmd = $null
    }

    if ($RunDefenderScan) {
        $defenderCmd = Get-DefenderCmd
        if ($defenderCmd) {
            $defender.executed = $true
            $defender.cmd = "$defenderCmd -Scan -ScanType 3 -File `"$Path`""
            & $defenderCmd -Scan -ScanType 3 -File $Path | Out-Null
            $defender.exit_code = $LASTEXITCODE
            if ($LASTEXITCODE -ne 0) {
                $errors += "defender scan failed with exit code $LASTEXITCODE"
            }
        } else {
            $errors += "defender scan tool not found (MpCmdRun.exe)"
        }
    }

    return [ordered]@{
        path = $Path
        present = $true
        size_bytes = [int64]$file.Length
        sha256 = $hash.Hash.ToLowerInvariant()
        signature = [ordered]@{
            status = [string]$sig.Status
            status_message = [string]$sig.StatusMessage
            signer_subject = $signerSubject
            signer_thumbprint = $signerThumbprint
            timestamp_subject = $timestampSubject
        }
        defender = $defender
        errors = $errors
    }
}

$resolvedInstaller = Resolve-ArtifactPath -Path $InstallerPath -FallbackGlob "dist/installer/You2Midi-Setup-*.exe"
$resolvedDesktopExe = Resolve-ArtifactPath -Path $DesktopExePath -FallbackGlob "dist/desktop/you2midi-desktop.exe"

$checks = @()
if ($resolvedInstaller) {
    $checks += (New-FileCheck -Path $resolvedInstaller -ExpectedPublisher $ExpectedPublisher -RequireSignature:$RequireSignature -RunDefenderScan:$RunDefenderScan)
} else {
    $checks += [ordered]@{
        path = "dist/installer/You2Midi-Setup-*.exe"
        present = $false
        errors = @("installer not found")
    }
}
if ($resolvedDesktopExe) {
    $checks += (New-FileCheck -Path $resolvedDesktopExe -ExpectedPublisher $ExpectedPublisher -RequireSignature:$RequireSignature -RunDefenderScan:$RunDefenderScan)
}

$errors = @()
foreach ($check in $checks) {
    if ($check.errors) {
        foreach ($e in $check.errors) {
            $errors += "$($check.path): $e"
        }
    }
}

$report = [ordered]@{
    generated_at_utc = (Get-Date).ToUniversalTime().ToString("o")
    require_signature = [bool]$RequireSignature
    expected_publisher = $ExpectedPublisher
    smartscreen_reputation = [ordered]@{
        automated = $false
        status = "manual_validation_required"
        notes = "SmartScreen reputation must be validated on a clean Windows VM with SmartScreen enabled."
    }
    checks = $checks
    issue_count = $errors.Count
    issues = $errors
}

$outDir = Split-Path -Path $OutputPath -Parent
if ($outDir) {
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
}
$json = $report | ConvertTo-Json -Depth 8
[System.IO.File]::WriteAllText([System.IO.Path]::GetFullPath($OutputPath), $json, [System.Text.UTF8Encoding]::new($false))

if ($errors.Count -gt 0) {
    $message = "Windows security verification failed:`n - " + ($errors -join "`n - ")
    if ($Strict) {
        throw $message
    }
    Write-Warning $message
} else {
    Write-Host "Windows security verification passed."
}

Write-Host "Report: $OutputPath"
