# ADR 995: Concurrent admission during registry cleanup

Status: implemented behind the existing cleanup operator flag; native acceptance
pending. Extends ADR 994. ADR 996 adds optional background continuation.

The original schema-5 cleanup holds every quota write through reader grace and
all cleanup/verification passes. The monotonic epoch barrier already prevents
old deletion workers from reaching current-epoch objects. Continuing writes in
that epoch does not weaken deletion authority, so the multi-minute admission
pause is unnecessary for newly started operations.

Publish schema 6 with the rotated references and cleanup checkpoint in the same
root CAS. Earlier binaries reject that schema. Current binaries permit ordinary
quota mutations throughout grace and scanning, preserving the checkpoint and
writing only current-epoch shards. Connections need not drain before cutover.
Inventory migration, policy changes and another epoch rotation remain blocked
until cleanup completes. Existing schema-5 operations retain their original
hold; operators must finish them before a new rotation upgrades the root.

Listing/deletion still uses the immutable barrier and bounded page validation.
After a definite root CAS conflict, re-read the root. If schema, epoch and the
entire original cleanup checkpoint match, merge only the advanced checkpoint
into fresh quota state and retry its CAS. Do not re-list or repeat deletes during
this retry. Preserve concurrent reservations, rate counters, grants, connections
and retirement accounting. An advanced/restarted operation fences the old
worker. Unknown acknowledgements require inspection rather than automatic retry.
Each call allows at most eight root CAS attempts and remains subject to its
request deadline; contention exhaustion returns busy with progress uncommitted.

Reader grace remains the maximum lease plus invocation timeout. It delays
deletion, not admission. The root remains a shared CAS bottleneck, so heavy
admission may delay cleanup checkpoint progress. Current-epoch superseded shards
remain until a later rotation; concurrent writes do not promise zero metadata
growth or an always-successful cleanup step. No new background worker or
production enablement is introduced. Provider and multi-node acceptance must
cover mixed versions, legacy holds, reader drain, busy retries, process loss,
delayed deletion and intact quota accounting while admission remains active.
