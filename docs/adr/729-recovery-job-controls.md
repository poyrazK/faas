# ADR-729: Recovery job pause, resume, and rate controls

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Add durable admission controls to existing routing and execution recovery jobs.
- **Why:** Operators observing renewed handler failures need to slow or stop further admissions while retaining their recovery selection and progress.

## Interface

Add `POST /v1/event-recoveries/{jobID}/pause`, `POST .../resume`, and
`PUT .../rate` with `{ "rate_per_second": 1..100 }`. They use existing
account ownership, deploy-write/admin scopes, MFA, request timeout, and no-store
responses. The Go, Node, and Python clients and CLI commands `events
recovery-pause`, `recovery-resume`, and `recovery-rate` expose these controls.
CLI mutations require `--yes`.

Responses expose the current top-level `rate_per_second` and optional
`paused_at`. `selection.rate_per_second` remains the original requested rate;
selection filters, selected items, replay identities, admission counts, and
execution observations retain their existing meanings.

Pause transitions running to paused; resume transitions paused to running.
Repeated pause/resume on the corresponding active state and setting the same
rate succeed without changing the control timestamp. Rate can change while
running or paused. Completed/cancelled jobs return 409. An expired active job is
cancelled with pending-item reason `expired`, commits that transition, and
returns 409; controls cannot revive it. Cancellation works for paused jobs.

## Serialization and pacing

Every control locks the same job row as the recovery worker and cancellation.
A pause waits for an in-flight item transaction. After it commits, no further
admissions occur until resume, although already queued routing retries and
handler executions continue. Memory stores perform the same transitions under
their mutex.

Controls preserve the one-second window start and permits already spent.
Lowering a rate can leave the spent count above the new rate; the schema bounds
that counter by the global maximum of 100 instead of the mutable current rate.
The worker admits another item only after that window expires or while the
counter is below the current rate. Rate changes and resumes never pull an
existing next-attempt time earlier. Capacity waits, legacy receipt-claim waits,
and scheduled pacing waits survive controls. Increasing the rate may therefore
wait until a previously scheduled window boundary (at most one second for a
pacing wait). Expired windows reset naturally on the next processed item.
Rapid pause/resume or rate changes cannot replenish spent permits.

## Quota, expiry, and retention

Paused jobs remain active for the three-job account quota, preventing pause from
creating unbounded retained selections. The original 24-hour expiry remains
absolute and is never extended by a control. Worker selection includes expired
running or paused jobs regardless of their next-attempt time or spent budget;
it cancels their remaining items without admitting anything. An active-expiry
index supports that selection. Only completed/cancelled metadata is pruned
under the existing 30-day policy.

The append-only migration adds a nullable pause timestamp, paused lifecycle
constraints, the relaxed spent-counter constraint, and the expiry index. The
clone registry keeps the new timestamp operational. Rollback refuses paused
jobs and a live window whose spent count exceeds its current rate; expired
windows may be normalized before restoring the old constraints.
