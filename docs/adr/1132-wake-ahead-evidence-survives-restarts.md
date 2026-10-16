# ADR-1132: Wake-ahead evidence survives gateway restarts

- Status: Accepted
- Date: 2026-10-12
- Related: ADR-977, ADR-196
- Amends: ADR-977 ("Evidence is per gateway process and starts empty after a restart")

## Context

ADR-977 wakes a service ahead of its caller only along edges this gateway
has measured, and only after `ServiceWakeAheadMinWakes` (20) observed caller
wakes. The learner lived in memory, so every gatewayd-internal restart —
including every platform deploy, which restarts every gateway — threw the
evidence away. An opted-in chain then paid serial restores again until each
caller had been woken 20 more times. For the idle-heavy apps wake-ahead
exists for, that can be days; deploys happen more often than that.

ADR-977 rejected durable statistics in Postgres because the gateway does not
write customer tables. That still holds.

## Decision

The learner's **finished** statistics — per caller: last wake time, decayed
wake count, and per target the decayed call and cold-call counts — are
written to a node-local file, `learner.json` in
`FAAS_GATEWAY_WAKE_AHEAD_STATE_DIR` (default `/var/lib/faas/wake-ahead`,
created by the gatewayd_internal_service role and allowed by the unit's
`ReadWritePaths=`).

- **When:** at most every `ServiceWakeAheadSnapshotInterval` (1 min), only
  when a follow window folded since the last write, and once more on
  shutdown (shutdown waits for it within the drain grace).
- **How:** JSON with a format version, written to a temp file in the same
  directory, fsynced, then renamed; mode 0600; capped at
  `ServiceWakeAheadSnapshotMaxBytes` (16 MiB, above the 4,096 callers × 16
  edges bound).
- **Restore:** at start, before traffic shapes the learner. A missing file is
  the ADR-977 cold start. A corrupt, oversized or foreign-version file is
  logged and ignored. A snapshot older than `ServiceWakeAheadSnapshotMaxAge`
  (7 days) is ignored, so a long-idle node does not resurrect a dead pattern
  while an idle weekend does not erase a live one. Restored values are
  validated (finite, non-negative, cold ≤ calls, no self-edges) and the
  ADR-977 bounds apply: the most recently woken callers and the strongest
  edges are kept. A caller the gateway has already observed keeps its live
  statistics.
- Open follow windows and live speculations are not persisted; they are
  seconds old.

Nothing else changes: thresholds, decay, the residency guard and admission
are exactly ADR-977's.

## Consequences

- A restarted or redeployed gateway predicts immediately for callers it had
  learned, instead of after 20 more wakes each.
- Evidence is still per node: a node that never saw a caller still learns it
  from zero, and nodes do not share snapshots.
- A failed write (full disk, missing directory) only loses persistence; it is
  logged and wake-ahead continues in memory.

## Rejected alternatives

- **Postgres** — unchanged from ADR-977: the gateway does not write customer
  tables, and a shared table would also need cross-node merge semantics.
- **Seeding from the Prometheus edge counters** (ADR-288). Those count all
  calls, not calls inside a caller's follow window or whether the target was
  parked, so they cannot reproduce the learner's statistics.
