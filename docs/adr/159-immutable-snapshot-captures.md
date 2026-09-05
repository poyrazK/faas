# ADR-159 · Immutable snapshot captures and reuse

- Status: Proposed
- Date: 2026-09-06
- Acceptance: snapshot park/wake and zero-residency gates (§14), including failed-publication metal regression.

## Context

A three-instance SSD load test exposed a failed park that published device state
before its memory upload timed out. The next restore read the previous memory
with the new virtio queue state and Firecracker rejected it. Stable deployment
keys make two independent object uploads unsafe: neither upload ordering nor a
longer deadline makes replacement atomic.

The existing snapshot row writer accepts one usable row per deployment and tier.
Repeated idle parks overwrite its blobs even though the row remains unchanged.
This also uploads hundreds of MiB unnecessarily and blocks the scheduler's app
lock during capture. Snapshots are disposable deployment caches (ADR-005), not
persistence of an invocation's latest in-memory state.

## Decision

New captures use `snap/<deployment>/[warm/]captures/<uuid>/{mem,vmstate}`.
The scheduler allocates a fresh capture UUID and emits the exact memory key only
after both writes succeed. Restore, prepositioning and garbage collection derive
the device-state sibling from that key. Legacy keys remain readable.

Retain an existing non-stale snapshot of the same Firecracker version and tier.
Idle park then destroys the live guest and releases its resources without
recapturing that tier. Missing or stale snapshots are rebuilt normally. Warm
capture still requires the existing plan, opt-in, age and request-count gates.
A new deployment or snapshot invalidation allows a fresh capture.

Imaged remains the sole snapshot row writer. If delayed notifications allow two
captures of the same tier, the first accepted row wins and the unused candidate
pair is deleted. Failed captures clean up their private objects with a bounded,
uncancelled cleanup context. Deletion resolves recorded generation keys, rather
than relying on a control-plane cache listing to enumerate compute-node files.

## Consequences and limits

An interrupted upload cannot overwrite a published capture. Repeated idle parks
avoid redundant snapshot serialization and registry uploads. Customer memory is
not durably checkpointed after every invocation; this matches the disposable
snapshot contract and must not be presented as durable application storage.

A process crash between upload and notification can still leave unreferenced
objects; cleanup failures must remain observable and registry orphan retention
remains an operational requirement. This change does not make PG notifications
a durable publication queue. Old scheduler binaries must not be restored after
new generation rows are published: first deploy readers (VMMD/imaged), then the
scheduler; rollback requires retaining generation-aware readers or invalidating
new rows through the owning service.
