# ADR-1354 · Isolated object-storage rehearsal fixtures

- **Status:** accepted; disabled pending native containment qualification
- **Date:** 2026-10-17
- **Decision:** Add a bounded, private S3 fixture adapter to warm contained command rehearsals. Each run has an immutable namespace and sealed, run-scoped SigV4 credentials. Fixture bytes and mutations live in dedicated rehearsal tables, never in managed or production buckets. There is no provider fallback.
- **Why:** Final object listings alone hide repeated uploads and application-produced observations cannot establish storage effects independently.
- **Consequences:** Pin identical initial bytes and metadata for both revisions. Durably admit requests before reading upload bodies, atomically persist successful writes with their completion receipts, and retain duplicate attempts. Fence admission before collection, join admitted requests, independently hash all final bytes, and remove all retained object bodies before complete evidence is committed. Lost requests, unsupported operations, exceeded bounds, expiration, and failed cleanup are inconclusive.
- **Rejected alternatives:** Reusing ordinary bucket bindings, forwarding requests to a customer endpoint, treating ETags as content digests, collecting only final state, or trusting command output as storage evidence.

The first adapter implements path-style, header-signed S3 PutObject, GetObject,
HeadObject, DeleteObject, HeadBucket and bounded ListObjectsV2 without pagination.
It supports a single logical bucket, 32 objects, 64 KiB per object, 256 KiB of
current content and 256 admitted requests per run. These are versioned evidence
format bounds, not new plan entitlements. Content type and bounded user metadata
are compared. Versioning, multipart and streaming uploads, copy, presigned URLs,
conditional and range requests, tags, encryption controls, storage notifications,
and provider-specific behavior are unsupported and cannot yield passing evidence.
This adapter executes storage behavior against a simulator; it does not qualify a
production storage provider or observe arbitrary guest filesystem writes.

Only the private bridge listens on port 10084. Each request resolves the current
VM caller afresh, then verifies the exact namespace credential and retained run
policy. The durable admission transaction shares the action run lock and checks
the fresh policy after locking. Sibling workloads do not inherit credentials in
this increment. The primary app and its contained commands share the namespace.
The initial deployment assumes one qualified private gateway placement. Namespace
creation waits for its listener incarnation to claim the capture window before
returning an acknowledgement. Replacement collectors cannot adopt old windows.
Collector errors are sticky for that incarnation; restart it after repairing the
fault. Sealing requires the original collector's durable closing acknowledgement
after all pending requests finish. Even an empty inventory cannot pass after its
collector disappears. Calls that never reach the private listener remain outside
this capture boundary and require recipe assertions of application outcomes.
Ordinary VMs receive no new network permission. Both firewall hooks must permit
the exact bridge/port for contained VMs; public and arbitrary destinations remain
denied. Builds remain outside this runtime adapter's containment scope.

All mutable resource state is serialized by a database row lock. Sealing changes
ready to sealing before waiting for pending admissions; completion may join while
sealing, but new admission cannot. A bounded wait failure discards evidence and
cleans up. Cleanup and evidence publication share one transaction, making an
ambiguous response retryable. Expiry cleanup operates independently of admission
gates, and deletion revokes pending work as well as stored bytes. Resource intent,
starting snapshot, completed receipts and terminal evidence are immutable. Live
resources block owner purge until cleanup, while terminal journals follow owner
retention.

Evidence includes release/run/app/resource/collector identity, pinned fixture and contract
hashes, initial/final key inventories, sizes, SHA-256 content digests and metadata,
and successful mutation receipts in mutation completion order. Comparison checks final
state and the multiset of mutation attempts, preserving duplicate writes while
ignoring scheduler order across independent operations. It does not compare
intermediate read timing or promise whole-application undo. Only reviewed code,
configuration and migration plans are promoted.

Explicit sibling capabilities and managed async completion extend this scope in
[ADR-1355](1355-rehearsal-managed-workers.md).
