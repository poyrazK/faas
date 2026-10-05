# ADR-628: Regional migration source retention and rollback completion

Date: 2026-10-05
Status: Host and transport implementation; native pause proof and scheduler integration pending.

## Context

Migration mutates the instance owner and wake ID. The legacy migration lease can
be replaced after expiry and cascades with instance deletion. It cannot retain
the original physical source or acknowledge a completed rollback after its RPC
reply is lost. Instance-only cancellation and automatic expiry resume are
insufficient for regional capacity and serving authorizations.

## Decision

Retain an immutable source binding in the exclusively owned persistent host quota
journal: exact runtime lookup key, migration lease token, complete physical permit,
original serving seat and immutable capture object keys. Require explicit source
metadata for regional capture.
Write that binding before irreversibly fencing the old serving seat and before
any capture side effect. A restart between binding persistence and fencing
finishes the fence before the journal becomes usable. Reject parallel physical
operations and competing migration leases. Never resume implicitly after a
regional capture error. Keep the original binding, lease and physical hold on
capture, persistence, cancellation or cleanup uncertainty.

Rollback requires the exact retained source and lease plus a fresh cell-issued
serving seat on the same complete physical permit. Retain its proposed seat
before native dispatch; retries cannot substitute another seat. Ordinary warm
resume, legacy cancellation, expiry and acknowledgement cannot bypass an
unresolved migration. Use the existing instance flight and native serving
completion path, including deferred restore hooks, readiness and current cell
authority. Persist successful rollback completion while its exclusive host
operation is still held, before publishing serving or returning the exact
instance/lease/placement acknowledgement. The RPC owner re-reads and matches
that persisted completion before cleanup or acknowledgement. A completed response can be replayed
after lease deletion or daemon restart with current authority and fences. An
empty live map never acknowledges an unfinished rollback. A new boot epoch
cannot execute or acknowledge the previous epoch's serving authorization.

Cleanup follows durable completion. Cleanup failures retain the legacy lease
and retry the same completion without repeating native resume. Expiry may clean
up objects and lease metadata after a known completed rollback, without VM work.
Unknown leases
cannot produce a regional acknowledgement. Extend protobufs additively and
provide explicit client/router capabilities with exact acknowledgement checks
and no legacy fallback. Preserve independent-app migration behavior.

This is not native pause/stop proof and does not sign or release any seat or
physical capacity. Before scheduler integration, verify the old seat's native
pause against stable process/resource identity, authenticate its release, and
issue/coalesce rollback under the app's durable quota lock. Recovery must
inventory the host journal even when the lease/instance row has disappeared;
unfinished recovered native processes remain quarantined. Source retirement
and expiry require that same original binding and verified native cleanup.
All accepted quota heads remain frozen. Runtime admission, billing, provisioning
and ingress to all selected healthy eligible regions still require their
remaining acceptance work.

## Validation

Exercise source retention before native work, crash between record and fence,
immutable bindings, old-seat refusal, competing leases, uncertain capture/resume,
exact retry, lost completion replies, restart and kernel epoch rejection. Check
legacy cleanup cannot bypass a retained regional source. Exercise canonical
protobuf transport, exact acknowledgement and capability refusal. Run affected
race suites, diff lint and repository policy checks. Dedicated native x86_64
Linux KVM test-metal, leakcheck and recovery remain required for lifecycle
acceptance; shared nested GCE portable tests cannot satisfy those requirements.
