# Windows Defender & SmartScreen Validation

## Objective
Verify release artifacts are:
- correctly signed
- timestamped
- clean in Microsoft Defender
- acceptable under SmartScreen on a clean machine

## Automated Checks
Script:
- `scripts/verify_windows_security.ps1`

Inputs:
- Installer: `dist/installer/You2Midi-Setup-*.exe`
- Desktop EXE: `dist/desktop/you2midi-desktop.exe` (optional)

Example (signed release gate):
```powershell
powershell -ExecutionPolicy Bypass -File scripts/verify_windows_security.ps1 `
  -InstallerPath dist/installer/You2Midi-Setup-v1.0.0.exe `
  -DesktopExePath dist/desktop/you2midi-desktop.exe `
  -RequireSignature `
  -ExpectedPublisher "Your Company, Inc." `
  -Strict
```

Report output:
- `dist/security/windows-security-report.json`

## SmartScreen Manual Validation (Required)
SmartScreen reputation cannot be fully validated offline in CI. Perform this in a clean Windows VM:

1. Use a fresh VM snapshot with SmartScreen enabled.
2. Download installer from the actual distribution channel (same URL users receive).
3. Run installer and record whether SmartScreen blocks/warns.
4. Complete install and first launch.
5. Capture screenshot and execution result in release evidence.

Expected for production:
- Publisher shown as signed publisher (no unknown publisher).
- No malware detection by Defender.
- SmartScreen warning rate tracked across releases (should trend down with reputation).

## Release Evidence
For each release tag, attach:
- `windows-security-report.json`
- SmartScreen VM screenshot(s)
- signer certificate subject + thumbprint used for signing
