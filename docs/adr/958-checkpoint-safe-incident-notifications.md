# ADR-958: Checkpoint-safe incident notifications

## Status
Accepted.

## Context
ADR-957 only retained current-pass lifecycle transitions. A CLI/SDK watch restart could lose an alert before downstream processing. Printing does not prove consumer acknowledgement.

## Decision
Add optional durable alert mode to stall-enabled watches, automatically enabled by a CLI checkpoint with stall detection. Keep an ordered outbox of pending transitions and saved active incidents in monitoring snapshots. SDK callers persist snapshots before delivery; CLI persists before emitting callback and final-check alerts. Replaying uses original incident IDs and sequences, continuing the saved notification namespace. Restore only after validating the frozen selection and saved queue/episode shape. Never trust saved live confirmation or local timing: previous active episodes emit confirmation_lost/watch_resumed, and new episodes require new fresh sampling.

Accept explicit exact incident-ID/sequence acknowledgements at initialization and synchronous SDK callbacks after progress. Validate acknowledgements atomically and reject duplicates within a batch, wrong-stream/future acknowledgements and conflicting identities for pending sequences. Already removed sequences in the same stream accept repeated acknowledgements as no-ops without retaining an unbounded acknowledgement ledger. CLI reads acknowledgements from a protected input file on resume/refresh. Remove only acknowledged pending entries; their removals become durable in the next/final saved snapshot. Failed/invalid initialization never replaces a CLI checkpoint. Pending notifications remain historical and do not prove current delivery state.

Bound storage through centralized CommitRecoveryAlertQueueMax=1024 slots and CommitRecoveryAlertSequenceMax=9007199254740991. Reserve one closing transition for every active incident, so every opening needs two free slots and terminal transitions cannot be silently dropped. Refuse new openings and stop with alert_queue_full when capacity/sequence reservations fail; retain existing queued transitions and sampled status for acknowledgement and restart. No eviction, implicit acknowledgement, unbounded history or network sink is introduced.

## Consequences
Delivery of checkpointed notifications is at least once: crashes between consumer processing and acknowledgement persistence can cause duplicates. Deduplicate by incident ID and sequence. No exactly-once claim or durability for notifications that have not been saved. Durable mode extends ADR-957's invocation-only identity/reset behavior; non-durable behavior remains unchanged. No backend, SQL, migration, receipt budget, recovery action or compliance registry changes are needed. Notification backpressure adds an explicit watch stop condition.
