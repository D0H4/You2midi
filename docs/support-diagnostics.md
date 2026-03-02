# Support Diagnostics Bundle

## Goal
Create a single zip file that support can request from end users.

Command:
```powershell
make support-bundle
```

Script:
- `scripts/bundle_support_logs.ps1`

## Contents
- `config/config.toml` (if present)
- `config/config.example.toml`
- `artifacts/artifact-manifest.json` (if present)
- `security/windows-security-report.json` (if present)
- `workspace/you2midi.db` (if present)
- `diagnostics.json` (system/env summary)

## Output
- `dist/support/you2midi-support-<UTC timestamp>.zip`

## Notes
- The bundle includes `YOU2MIDI_*` environment variables and `PATH`.
- Treat bundles as sensitive operational data.
- If you need to skip DB collection:
  - `powershell -ExecutionPolicy Bypass -File scripts/bundle_support_logs.ps1 -IncludeWorkspaceDb $false`
