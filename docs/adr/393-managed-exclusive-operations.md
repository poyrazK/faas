# ADR-393 · Managed exclusive operations

- **Status:** implementation in progress; internal until all acceptance gates pass
- **Date:** 2026-09-30
- **Decision:** Add account-owned operation policies shared by explicitly authorized
  apps and account-scoped Jobs. A durable operation key has a monotonically increasing ownership generation,
  independent of invocation IDs and delivery attempts. PostgreSQL serializes admission,
  ownership, renewal, recovery, and platform-controlled result/effect commits.

## Contract

An operation lane consists of an account-owned policy, trusted environment context,
verified platform-tenant identity (for tenant-scoped policies), and a bounded typed
business key. Account and customer scope never come from a caller-supplied key prefix.
Participation is explicit; policy members must belong to the same account.

`queue` retains distinct operations in FIFO admission order. `reject` rejects a busy
lane atomically, including earlier accepted work. `join_existing` requires a separate
equivalence identity and identical normalized target/input; a concurrency key alone
never establishes equivalence. Submission idempotency is resolved before contention.
Policy settings are captured on acceptance. Existing app-local work policies retain
their present semantics.

Each new ownership grant increments the lane generation and mints an opaque token.
Renewal preserves generation and requires an unexpired matching owner/token. Expiry
cannot be undone by the previous owner. Database time is authoritative. Result writes,
retry, release, and supported effect insertion verify the current ownership in the
same transaction as their write. Stale work cannot release a successor's capacity.
Generation counters survive receipt cleanup and policy retirement.

Scheduler-managed heartbeats are bounded by an attempt deadline and tied to live
execution. Losing renewal cancels local execution where possible and always revokes
platform commit authority. No assertion is made that arbitrary external effects stop.

Ownership is bound to a host-controlled VM incarnation. Active exclusive work inhibits
parking. Restoration/replacement invalidates the old incarnation before execution is
exposed and requires a fresh delivery grant. Resume hooks refresh context but clearing
guest memory is not the security boundary. Direct external database/provider effects
require a compatible transaction/adapter or idempotency mechanism; a pre-call lease
check does not close the check-to-use race.

## Integration and acceptance

Manual async calls, deployment-attached AppTasks, manual Job runs, HTTP and
command crons, recurring Job schedules, and verified inbound webhooks enter one admission
path. Queue/inbox and broker adapters use the same ownership store. Job and
AppTask adapters carry ownership generations into durable result transitions.
Inspection exposes authorized receipts and ownership status,
never renewal secrets. Manifest, CLI, OpenAPI, SDK, audit, bounded metrics, quotas,
recovery documentation, and capability evidence accompany the public surface.

Acceptance uses real PostgreSQL transactions for cross-app contention, tenant
isolation, idempotency replay, independent contention modes, renewal/expiry races,
stale completion and stale release, result/effect atomicity, quota reconciliation,
and generation preservation. Native KVM tests must restore a stale worker after a
successor is granted authority and reject its renewal and all supported commits.
