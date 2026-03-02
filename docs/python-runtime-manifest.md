# Python Runtime Manifest (Remote Mode)

Desktop app can provision Python runtime from CDN at startup using:
- `runtime/python-runtime.json`

## Manifest schema
```json
{
  "version": "20260302-1",
  "archive_url": "https://cdn.example.com/you2midi/runtime-python-20260302-1.tar.gz",
  "archive_sha256": "<sha256-hex>",
  "scripts_rel_path": "Scripts",
  "archive_type": "tar.gz"
}
```

Fields:
- `archive_url`: Required. Runtime ZIP URL.
- `archive_sha256`: Optional but strongly recommended.
- `scripts_rel_path`: Optional. Defaults to `Scripts` on Windows (`bin` on non-Windows).
- `archive_type`: `zip`, `tar.gz`, or `auto`.

## Runtime archive expectations
- Archive must contain `Scripts/python.exe`.
- For full transcription flow, archive should also include:
  - `Scripts/transkun.exe`
  - `Scripts/yt-dlp.exe`

## Build integration
- `scripts/desktop_build.ps1` remote mode is enabled by default.
- Provide URL via:
  - `-PythonRuntimeArchiveUrl "<url>"`
  - or env var `YOU2MIDI_PYTHON_RUNTIME_URL`
