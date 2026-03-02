# ADR-002: Engine Selection Strategy

- Status: Accepted
- Date: 2026-02-27
- Owner: You2Midi

## Context

You2Midi supports multiple transcription runtimes:

- Primary target: `transkun` (piano-specialized)
- Secondary target: `neuralnote` (fallback engine)

Desktop distribution may include bundled runtime paths, but those paths can fail due to missing/corrupt binaries or environment differences. Service behavior must remain predictable and recoverable.

## Decision

Engine selection is defined in two phases:

1. Startup resolution (`main.go`)
   - Resolve binary candidates with health checks (`runner.ResolveBinary`).
   - For each runtime, try configured path first, then PATH fallback names.
   - Set engine wiring:
     - If `transkun` is available: `transkun` is primary, `neuralnote` (if available) is fallback.
     - If `transkun` unavailable and `neuralnote` available: promote `neuralnote` to primary.
     - If both unavailable: keep service up for diagnostics/repair; jobs may fail until runtime repair.

2. Per-job execution (`internal/service/pipeline.go`)
   - Execute with primary engine first.
   - If transcription fails with `ENGINE_NOT_FOUND` and fallback engine exists, retry same input with fallback engine.
   - Persist both preferred/actual engine in logs (`preferred_engine`, `actual_engine`).

## Consequences

### Positive

- Runtime launch failures can degrade gracefully instead of hard startup failure.
- Users keep a working conversion path when only one engine runtime is healthy.
- Service-level fallback remains deterministic and testable.

### Negative

- Startup logic is more complex (runtime probing + fallback wiring).
- Behavior depends on local PATH if configured bundled runtime fails.

## Alternatives Considered

1. Hard-fail startup when primary engine is missing.
   - Rejected: harms usability in partial-runtime environments.
2. Always use neuralnote first.
   - Rejected: conflicts with piano-specialized target quality strategy.
3. Random/score-based dynamic selection on each job.
   - Rejected for now: unnecessary complexity before stable baseline metrics.

## Implementation Notes

- Runtime resolution and fallback logging: `main.go`
- Binary candidate health resolution: `internal/runner/runner.go`
- Service fallback on `ENGINE_NOT_FOUND`: `internal/service/pipeline.go`
