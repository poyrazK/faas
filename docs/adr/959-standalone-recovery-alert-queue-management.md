# ADR-959: Standalone recovery alert queue management

## Status
Accepted.

## Context
ADR-958 required restarting a live watch to acknowledge checkpointed alerts. Operators need to drain queued notifications during outages without live API access or resetting saved incident episodes.

## Decision
Add local `commit recovery-alerts --checkpoint PATH`: inspect the validated historical outbox and capacity, replay pending transitions as ordered JSON lines, or apply an explicit acknowledgement file. All operations acquire the existing exclusive checkpoint sidecar lock before reading. Acknowledgements reuse the durable stream's transactional validation and idempotency rules and persist through the existing atomic checkpoint writer. Invalid queues or acknowledgement batches leave the checkpoint unchanged. Replay does not acknowledge, poll, emit new transitions, or change saved incident confirmation.

Expose the same local acknowledgement utility in the Go SDK. Validate queue bounds, event shapes, episode lineage, and exact acceptance/recipient bindings against the checkpoint's saved selection. This is local consistency validation, not independent verification against a reviewed plan or live platform state. Preserve monitoring evidence, active incident reservations, stream identity, sequence, and observation timestamps. Inspection reports pending count, reserved closing slots, total capacity, and remaining slots; sequence exhaustion remains a separate watch constraint.

## Consequences
Operators can drain a full queue before restarting monitoring. At-least-once replay still requires downstream deduplication by incident ID and sequence. A watch holding ownership blocks local queue operations. No network, backend, SQL, recovery action, or compliance registry changes are needed.
