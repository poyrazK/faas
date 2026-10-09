# ADR-903 · Object-storage entity outbox commit contract

- **Status:** accepted for internal engine implementation; delivery and qualification pending
- **Date:** 2026-10-09
- **Decision:** Extend ADR-712's pure engine transition with bounded outgoing intents retained inside the immutable snapshot. Publish business state, alarm, request receipt and outgoing intents through the same fenced manifest CAS. Add private exact-scope observation; keep guest protocol v1 closed to outbox messages until a delivery worker and destination admission exist.
- **Why:** A committed reservation and its confirmation intent must survive together. Sending during the callback can escape a rejected or obsolete state transition; enqueueing separately after commit can lose work on process death.
- **Consequences:** Entity state and pending outgoing intents have no SQL authority. This slice records work without dispatching it. It does not launch customer messaging or establish native/live-provider qualification.
- **Rejected alternatives:** Sending in the callback breaks the pure transition contract. A separately written authoritative queue cannot atomically commit with the entity manifest. Unbounded inline messages defeat snapshot and restore limits. A second custom webhook sender duplicates Gregale's delivery machinery and destination controls.

## Publication and recovery

The private Go engine accepts `Transition.Outbox`, containing a canonical UUID
of a registered app webhook, a nonempty bounded event type and a JSON payload.
The engine checks shape, not registration or entitlement. It holds no webhook
credentials and performs no networking. Guest transition decoding rejects the
field, including an empty array, and ordinary guest views never include pending
messages. Callback authors still compute pure transitions.

Messages append to the restored snapshot; omitting outgoing intents preserves
all pending work. Each message has a deterministic UUID derived from the full
account/app/environment/tenant/namespace/key scope, committed state version and
zero-based batch ordinal under a versioned UUID namespace. Identical messages
at different positions or committed versions have different identities. A
receipt replay returns the original result before computing or appending work,
including when the queue or storage cap is full.

Only the successful manifest CAS makes a snapshot and its messages committed.
Uploaded candidates are not replay or delivery evidence. A rejected CAS,
obsolete owner, cancelled handler or failed upload publishes no new intent.
After a lost publication acknowledgement, retrying the same request identity
and payload resolves through the committed receipt without duplicating messages.
Restart/takeover restores pending messages through authenticated snapshot roots,
never LIST results. `Manager.PendingOutbox` is a bounded private observation,
not dispatch authority or a delivery acknowledgement. Corrupt/missing state
fails closed; an observed reclamation race is retryable.

## Bounds, accounting and upgrade

All limits live in `pkg/api/limits.go`: at most 16 outgoing messages per
transition, 128 pending messages per entity, 64 KiB of JSON payload per message
and 256 KiB of encoded pending messages including identity/intent metadata.
The existing 1 MiB snapshot ceiling also applies. Validation and projected
committed-storage cap checks finish before any candidate upload. A full queue
rejects additional outgoing work atomically while allowing transitions that
preserve pending work and fit the storage cap.

Snapshots containing messages use schema 3 and include their bytes in existing
`SnapshotBytes` accounting. No independent quota authority or new object path
is introduced. Receipt migration, inventory, generation-fenced collection and
subsequent transitions preserve this authenticated snapshot content. Retaining
the current snapshot retains its pending messages; superseded snapshots and
uncommitted candidates can be reclaimed under the existing barrier contract.

Manifest writers use schema 5. Readers accept manifest schemas 1–5 and snapshot
schemas 1–3; older snapshots cannot carry pending messages, and schema-3
snapshots require a schema-5 manifest. Alarm retry reservations retain their
schema-4-or-newer requirement. Stop all older entity callers, alarm and
maintenance workers before upgrade. Older binaries reject new manifests;
downgrading requires an explicit storage migration that preserves pending work
and alarm reservations. Bucket lifecycle deletion remains disabled.

## Next milestone and acceptance

A future relay must reserve work durably, revalidate the current entity and
account/app/environment/tenant admission, and resolve the registered webhook
through existing destination/signing controls. Acceptance into Gregale's
delivery ledger must deduplicate by the stable message identity before a
fenced acknowledgement removes pending work. Repeating the existing public
outbox POST alone is insufficient because it can enqueue another delivery.
A lost relay acknowledgement or worker crash must leave restartable work.
Delivery is at least once; recipients must deduplicate before non-idempotent
effects. Exactly-once external effects are not promised. Retry/backoff,
inspection, dead letters/replay and eventual index discovery belong to that
delivery milestone, not to this engine-only commit contract.

CI failure cases cover atomic publication, rejected/uncertain writes, receipt
replay, takeover fencing, retained work after cleanup, snapshot accounting,
scope/ordinal identity, bounds and corrupt restore. S3 and native GCS SDK wire
fixtures exercise pending-message restoration/replay. Native microVM and real
bucket crash/partition/latency/cost evidence remain with the dedicated testing
agent; fixtures do not qualify those environments. The preview remains disabled
by default.
