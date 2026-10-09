# ADR-826: Telemetry coverage for route removal approvals

## Status

Accepted

## Context

Empty request telemetry can mean a quiet route, disabled collection, a failed
publisher, or rate-limited ingestion. Metrics alone cannot establish a complete
observation window for the server removal gate.

## Decision

The trusted gateway reports a durable heartbeat through apid's private gRPC
RequestTelemetry service at startup and every publisher tick, including idle
and disabled collection. The receipt records compute-node name, process boot
ID, monotonic sequence, enabled state, sampling basis points, cumulative
publisher/recorder loss, pending rows, source time, and server receipt time.
No customer API can publish or override coverage. Unknown nodes, invalid
reports, replays, out-of-order reports, and excessive source clock skew fail.

Continuous coverage begins at server receipt time. A boot change, counter
change, or heartbeat gap over 30 seconds resets it. Disabled or sampled
collection and pending delivery invalidate it until healthy reporting resumes.
Only explicitly accepted rows count as shipped. Failed or incomplete batches,
including rate-limited rows, exhaust bounded retries and count as loss. The
existing event IDs make replayed accepted writes idempotent.

Removal approval and subsequent positive traffic transitions require continuous,
unsampled, fresh coverage from every active compute node, including nodes with
no observed requests. This conservative fleet-wide requirement can block an
app because another app lost telemetry; per-app coverage is future work.
Membership and coverage rows are locked through the traffic/approval write.
The heartbeat writer does not acquire app locks. Missing or stale coverage
never becomes silence. Nodes must be administratively deactivated when retired;
a stopped gateway on an active node remains a blocker.

Approval windows end at the UTC minute boundary at least two minutes before
approval, allowing minute-bucket aggregation to settle. The grace period must
fit within plan telemetry retention with three minutes of margin. Approval age
still starts when the server issues the receipt, so short receipt lifetimes
remain supported. A later gap invalidates existing receipts and requires a new
full healthy grace period and approval. Check responses use stable coverage
blockers and existing next-actions/deadline fields; no public API shape changes.

## Consequences

- A route with no events can be distinguished from missing delivery evidence.
- Gateways and apid must support coverage before enforced removal can proceed.
- Heartbeat history is bounded to one row per registered node; losses cannot be
  erased by a cumulative-counter reset without restarting the grace period.
- Coverage proves delivery of recorded completed-request events, not behavioral
  equivalence or customer migration. Requests can arrive after a check, and
  in-flight requests can complete later. Observed-only acknowledgement remains.
