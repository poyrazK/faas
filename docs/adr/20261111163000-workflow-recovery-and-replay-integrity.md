# Workflow recovery and replay integrity

Status: Accepted

## Context

The workflow audit found tenant-unscoped start idempotency, reuse of ordinary
HTTP attempt numbers after recovery, and terminal code-workflow responses that
could omit recorded history. These violate the isolation, retry, and durable
replay guarantees of ADR-081 and ADR-20261021204307.

## Decision

Start idempotency is scoped by app, workflow, and platform tenant. NULL is the
account-owned namespace and remains unique. Tenant binding is checked before
returning a replay, including after a tenant is suspended or unlinked.

An HTTP attempt with an unknown outcome consumes its attempt number. Recovery
closes the attempt record and never decrements its number. Starts reject
reused numbers and expired leases, including stale pre-start snapshots. Completion and retry
writes must match the current running step and attempt, and may not use an
expired lease. Existing resume-generation fencing also remains in force.
Repeated recovery counts toward the configured retry budget. Side-effecting
handlers must continue to use their stable logical-step idempotency key.

Code-workflow SDK responses gain optional `replayed_ops`, the count of journal
entries visited by the invocation. The engine requires the full journal to be
visited before accepting a terminal response. Older SDKs remain readable, but
may not omit pending waits or retrying operations from terminal responses.
Detecting omitted, already-settled history requires the updated SDK.

The 128-byte operation-name limit applies to the caller-supplied base name.
SDK-generated repeats may append a canonical `#2` through `#1000` suffix (the
existing global operation-count limit). This preserves accepted base names and
keeps generated identifiers deterministic without truncation or collisions.

The exact event payload `{"timeout":true}` remains reserved for internal wait
timeouts, as already enforced by simulation. External events and callbacks
reject it consistently before any durable receipt.

## Compatibility and rollout

Apply the expand migration before the control-plane update. It builds the new
index concurrently while retaining the old uniqueness constraint. Apply the
contract migration in a full release after the corrected API reaches every
replica; tenant namespaces can then reuse keys. Old API binaries still contain
the original unscoped lookup. NULL tenants retain account-owned uniqueness.
The contract downgrade refuses if different tenants have reused a key; reconcile
those keys before downgrading the database. Interrupted concurrent index builds
are rebuilt on retry, including during downgrade. Upgrade SDKs afterward; the new
cursor is additive and older engines ignore it. Existing valid repeat names
remain accepted. No plan quotas or retry defaults change.

Internal workflow request and response encoding disables HTML escaping while
retaining transport bounds. SDKs bound errors as well as successful results;
PostgreSQL-bound error text is normalized and truncated on UTF-8 boundaries.
