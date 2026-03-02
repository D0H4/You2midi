# ADR-001: Runtime Boundary

- Status: Accepted
- Date: 2026-02-27
- Owner: You2Midi

## Context

You2Midi supports both desktop usage and web/API usage while reusing the same business logic:

- Desktop runtime uses a local Go process and subprocess tools (`yt-dlp`, transcription engine binaries).
- Web/API runtime exposes HTTP endpoints for remote job submission and retrieval.
- Core pipeline concerns (job state, retries, cache, workspace lifecycle, metrics) must stay consistent across runtimes.

Without a clear runtime boundary, UI transport code (Wails/HTTP) can leak into core logic and cause duplication.

## Decision

Define runtime boundary as:

1. `internal/service` is the single orchestration layer for job lifecycle, retries, fallback, cache, and cleanup.
2. Runtime adapters are thin:
   - HTTP adapter: `internal/api/http`
   - Desktop adapter: Wails desktop entry/runtime shell (current desktop app bootstrap + future bindings)
3. External tools/engines/storage are accessed through domain interfaces and adapter packages:
   - Engines: `internal/adapter/engine/*`
   - Downloader: `internal/adapter/downloader/ytdlp`
   - Storage: `internal/adapter/storage/sqlite`
4. Runtime-specific concerns stay outside service core:
   - Process bootstrap
   - Transport/auth concerns
   - Installer/packaging concerns

## Consequences

### Positive

- Single business path for both desktop and API runtime reduces DRY violations.
- Retry/cancel/fallback behavior remains consistent regardless of transport.
- Testing can focus on service layer with fakes (`runner.FakeRunner`, fake repos).
- Packaging/runtime changes (installer, Wails shell) are isolated from pipeline logic.

### Negative

- Additional adapter layer introduces upfront structure and files.
- Runtime bootstrap must explicitly wire all dependencies.

## Alternatives Considered

1. Split service logic by runtime (desktop service vs web service): rejected due to high duplication risk.
2. Put transport logic directly into service package: rejected due to boundary leakage and harder testing.

## Implementation Notes

- Current implementation matches this ADR:
  - Core orchestration: `internal/service`
  - HTTP transport: `internal/api/http`
  - Runtime tool adapters: `internal/adapter/*`
  - Entry composition: `main.go`
