# ADR-353 · Observe snapshot publication at the durable row boundary

- **Status:** accepted
- **Date:** 2026-09-28
- **Builds on:** ADR-074 and ADR-352

## Decision

The `app.warm_snapshot_promoted` audit event belongs to imaged, the sole
snapshot-row writer. Schedd sends capture-time warm gate evidence with
`snapshot_written`; imaged emits the account-scoped event with the new
snapshot ID only after `PublishSnapshotIfRuntimeFresh` inserts a warm row.
An existing row found on replay does not produce another promotion event.
The event remains best effort under the established audit policy.

Imaged exposes `imaged_snapshot_publication_total{tier,outcome}` with two
tiers (`init`, `warm`) and seven outcomes (`published`, `duplicate`,
`stale_config`, `rejected_ram`, `rejected_ephemeral`, `policy_error`,
`write_error`). The fourteen zero-valued series
are registered at startup. A duplicate counts each delivered replay;
`published` counts a newly durable row, including a new row replacing an
unrestorable legacy row. The metric contains no app, deployment, or
snapshot identifier.

## Rationale

Schedd's successful capture and notification send do not prove imaged
accepted the row. The notification can be delayed behind a runtime
configuration change, redelivered, or rejected for RAM incompatibility.
ADR-352 makes row insertion the freshness decision. Recording promotion
at the same boundary prevents a false success audit for those cases and
provides the snapshot ID for investigation.

The capture-time request count and warm thresholds travel in the notification
because app settings can change while delivery waits. Older notifications
without those optional fields remain publishable; their audit data leaves
the unavailable capture evidence null rather than substituting current
settings. The publication outcome counter identifies rejected and replayed
notifications without relying on audit write success.
