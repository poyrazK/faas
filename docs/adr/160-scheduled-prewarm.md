# ADR-160: Scheduled demand-window prewarm

Status: proposed (initial implementation)

## Context

An app may be parked when a known traffic window begins. A request-driven
wake then pays the restore/cold-boot latency on the first request. The existing
`min_instances` floor is permanent and billable, so it is not a suitable
representation for a temporary calendar or prediction.

## Decision

Persist a temporary `prewarm_intents` row with an app, desired capacity, the
demand-window start (`wake_at`), expiry, trigger, and lifecycle status. The API
creates and cancels intents; schedd claims pending rows during a 60-second lead
window and calls the existing coordinated wake path. That path remains the
authority for plan limits, ledger capacity, placement, RAM, wake-rate limits,
and snapshot-vs-cold-boot selection.

The first vertical slice exposes:

* `POST /v1/apps/{slug}/prewarm` with `{count,wake_at,expires_at}`;
* `GET /v1/apps/{slug}/prewarms` for lifecycle read-back; and
* `DELETE /v1/apps/{slug}/prewarms/{id}` for cancellation.

The schedd drain is an exact opt-in during rollout: set
`FAAS_PREWARM_ENABLED=1` after applying the migration. The API can accept
intents before the drain is enabled; they remain `pending` until schedd is
started with the flag.

Claims are atomic (`FOR UPDATE`-equivalent in MemStore), so a schedd restart or
multiple schedd workers cannot fire the same intent twice. `count` is a target
capacity, not an additive wake count; the engine computes the current ledger
delta and admits bounded batches. `admitted_count` records the actual result.
A failed admission is recorded as terminal `failed`; callers can schedule a
replacement intent.

The API emits `prewarm.scheduled`; schedd emits `prewarm.fired` with the
admitted count and outcome. Cancellation is allowed only while an intent is
still pending.

The API and schedd expose the same bounded `prewarm_intents_total{event}`
lifecycle vocabulary (`scheduled`, `succeeded`, `partial`, `failed`,
`expired`, and `cancelled`). Schedd also exports admitted-instance totals and
fire-offset/intent-age histograms. Each scheduler tick terminalizes any
pending row whose expiry has passed as `failed` with `outcome=expired` and
emits `prewarm.expired`, so a missed window cannot remain pending forever.

## Consequences

This slice supports explicit calendar/API scheduling. Cron-derived intents and
rule-based pattern detection can use the same store and scheduler contract
without adding another VM lifecycle path. It does not yet infer demand from
history or provide a first-class cron option; those are follow-up producers.
