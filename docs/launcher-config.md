# Launcher Config

Path:
- `dist/desktop/launcher-config.json`

Example:
```json
{
  "github_repo": "owner/repo",
  "github_api_base": "https://api.github.com",
  "installer_asset_pattern": "^You2Midi-Setup-.*\\.exe$",
  "patch_asset_pattern": "^you2midi-patch-.*\\.zip$",
  "app_executable": "you2midi-desktop.exe",
  "updater_executable": "you2midi-updater.exe",
  "install_dir_candidates": [
    "%ProgramFiles%\\You2Midi",
    "%LOCALAPPDATA%\\Programs\\You2Midi"
  ],
  "request_timeout_seconds": 30
}
```

Notes:
- `github_repo` must be `owner/repo`.
- Launcher behavior:
  - Not installed: prompts for first install and downloads installer asset from latest GitHub release.
  - Installed: checks latest patch asset and runs updater before launching desktop app.
