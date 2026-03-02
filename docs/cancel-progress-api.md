# Cancel & Progress API

## Endpoints

- `POST /jobs/{id}/cancel`
  - Purpose: cancel queued/running/retrying jobs.
  - Success: `200` with latest job snapshot.
  - Not found: `404`.
  - Invalid state (already terminal): `409`.

- `GET /jobs/{id}/progress`
  - Purpose: return polling-friendly progress snapshot.
  - Success: `200` with `JobProgress`.
  - Not found: `404`.

## JobProgress Contract

- `job_id`: job identifier.
- `state`: canonical job state (`queued|running|retrying|completed|failed|cancelled`).
- `stage`: coarse UI stage:
  - `queued`
  - `retrying`
  - `downloading`
  - `transcribing`
  - `finalizing`
  - `completed`
  - `failed`
  - `cancelled`
- `progress_pct`: integer `0..100`, stage-based estimate.
- `cancelable`: `true` unless terminal state.
- `attempt`, `max_attempts`, timing fields and error fields for diagnostics.

## Polling Guidance

- Recommended polling interval: `500ms ~ 2s`.
- Stop polling when `state` is terminal (`completed|failed|cancelled`).
