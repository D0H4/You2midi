# Desktop Release Runbook

## Goal
Package a Windows desktop release with:
- Wails desktop executable (`you2midi-desktop.exe`)
- Updater executable (`you2midi-updater.exe`)
- Launcher executable (`You2midi.exe`)
- Installer (`Inno Setup`)
- Artifact SHA256 manifest
- Post-build smoke check

## Prerequisites
- Go 1.24+
- Node 20+
- Wails CLI
  - `go install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0`
- Inno Setup (`iscc`) for installer build

## Local Commands
1. Build desktop executable
   - `make desktop-build`
   - Set GitHub repo for launcher auto install/update:
     - PowerShell: `$env:YOU2MIDI_GITHUB_REPO = "owner/repo"`
   - Remote runtime mode (default): set `YOU2MIDI_PYTHON_RUNTIME_URL` to a ZIP URL first.
   - Legacy bundled venv mode: `powershell -ExecutionPolicy Bypass -File scripts/desktop_build.ps1 -UseRemotePythonRuntime:$false`
2. Build optional lightweight patch package
   - `make patch-build`
3. Build backend smoke binary
   - `go build -o dist/desktop/you2midi.exe ./`
4. Run smoke test
   - `make smoke-postinstall`
5. Generate manifest
   - `make manifest`
6. Verify third-party license compliance
   - `make license-check`
7. Build installer
   - `make installer`
8. Run Windows security check report
   - `make security-check`
9. Generate support diagnostics bundle
   - `make support-bundle`

## Code-Signing Hook
Set an Inno Setup sign command before `make installer`:

```powershell
$env:INNO_SIGNTOOL = "signtool.exe sign /fd sha256 /tr http://timestamp.digicert.com /td sha256 /a /f C:\cert.pfx /p <password> `$f"
```

Then run:

```powershell
make installer
```

## CI Workflow
- File: `.github/workflows/desktop-release.yml`
- Trigger:
  - Manual (`workflow_dispatch`)
  - Tags (`v*`)
- Behavior:
  - Always builds backend smoke binary and runs health smoke
  - Builds installer only when Wails desktop artifact exists
  - On tag builds, enforces signed-release security verification (`EXPECTED_SIGNER_SUBJECT` required)
  - Tag builds fail if Wails desktop artifact is missing

## Windows Security Validation
- Automated report script:
  - `scripts/verify_windows_security.ps1`
- Manual SmartScreen validation guide:
  - `docs/windows-security-validation.md`

## Runtime Launch Fallback
- On startup, runtime binaries (`python`, `transkun`, `neuralnote`, `yt-dlp`) are health-checked.
- If a configured bundled path fails, the app attempts a fallback to PATH binary names.
- Fallback events are logged with `runtime fallback activated`.
- In remote runtime mode, desktop app reads `runtime/python-runtime.json` and auto-downloads the runtime archive on first launch.

## Launcher Flow
- `You2midi.exe` is the user entrypoint.
- If app is not installed:
  - Prompts user and downloads the latest `You2Midi-Setup-*.exe` from GitHub Releases.
- If app is installed:
  - Checks latest patch (`you2midi-patch-*.zip`) from GitHub Releases.
  - Runs updater and then launches desktop app.

## Migration Safety
- Config schema is version-gated via `schema_version` (`internal/config`).
- Workspace DB schema is versioned via `schema_meta` and applied in a single transaction (`internal/adapter/storage/sqlite`).
- If a migration step fails, SQLite transaction rollback prevents partial schema updates.
