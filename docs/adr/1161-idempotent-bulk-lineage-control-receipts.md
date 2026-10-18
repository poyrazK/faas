# ADR-1161: Idempotent bulk lineage control receipts

Status: Accepted
Date: 2026-10-13
Related: ADR-1155, ADR-1158, ADR-1160

## Decision

Extend pause/resume bodies with optional nonzero command_id. Command UUIDs are
scoped to one subscription/job incarnation, fenced by the existing original
created_at guard. Preserve legacy desired-state controls when the field is absent.
For UUID-keyed commands, acquire the existing job lock, validate live ownership,
retention and creation identity, and look for an earlier receipt before validating
current state or applying a transition. Same UUID/action/creation guard returns
its immutable receipt with current job observations, without applying the action.
An opposite action returns 409 `event_lineage_retry_control_command_changed`;
a changed job creation guard retains `event_lineage_retry_job_changed`.

Persist successful new commands, including no-op acknowledgments, in
`event_execution_lineage_retry_control_receipts`. Change job state and append the
receipt in one transaction under the same job lock; rollback leaves neither change.
The memory store mirrors this under its mutex, validates serialization before
committing and returns detached receipt values. New commands on completed/cancelled
jobs still fail; an earlier successful command remains repeatable after subsequent
controls, cancellation or completion while the original job is retained.

Receipts contain metadata only: command/action/target/creation identity, sequence,
original result state, selected/processed counts, observed job update time,
recording time and original job expiry. They do not copy case selections/results
or event payloads. Attach control_receipt to UUID-keyed control responses only;
outer job fields are current observations and the receipt is immutable original
evidence. Do not persist that response attachment in the job snapshot. Ordinary
GET/create/cancel and controls without a command ID retain their existing shape.

Allow at most 128 command receipts per retained job. The job lock assigns the next
increasing sequence from the journal maximum; ordering depends on sequence, not
wall-clock timestamps. Existing UUID repeats spend no slot, including at the cap.
A new UUID over the cap returns 429 `event_lineage_retry_control_limit` without
changing state. Legacy controls/cancellation remain available. Do not prune
individual receipts early, since that would permit reapplication of old commands.
Receipts expire at the job's original deadline and cascade when it is physically
pruned/deleted; memory job pruning removes its receipt map. The per-job bound also
bounds cascading cleanup work. No pause, repeat or receipt extends retention.

Expose read/MFA GET on retry-jobs/{requestID}/controls with a required original
creation timestamp, ascending sequence pagination, default 50/max 200 and an
exclusive after cursor. PostgreSQL uses a read-only repeatable-read snapshot to
validate scope/incarnation and list records consistently; memory uses its mutex.
An existing retained job with no receipts returns an empty array. Missing/expired
jobs return 404 and creation mismatch 409. Pages are live append-only observations;
a cursor does not reserve future commands or retention.

## Consequences

A lost-response pause A can be repeated after resume B without pausing work again.
The response carries A's recorded paused result and the job's current pending state,
so clients can distinguish command acceptance from current state. Failed requests
and legacy controls are not journaled; history is a bounded successful-command
ledger, not a complete actor audit or permanent archive. Cancellation remains its
existing terminal operation without a command journal. Apply the receipt migration
and upgrade API writers before sending command IDs; older strict API decoders
reject the new field. Downgrade deliberately refuses while receipts remain, rather
than silently deleting deduplication evidence or allowing replayed controls.
