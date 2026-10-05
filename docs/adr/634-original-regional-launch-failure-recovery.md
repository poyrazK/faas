# ADR-634: Original regional launch failure recovery

Date: 2026-10-05
Status: Implementation in progress; native acceptance pending.

## Context

ADR-633 retains the original physical permit and native lookup key, but wake and
prime failure paths still release reservations and mark rows terminal after an
uncertain RPC. Recovery can accept a native stop without completing scheduler
state. A warm runtime can retain its original physical permit across newer
serving attempts, so that permit alone cannot authorize failed-launch cleanup.

## Decision

Captured initial wake, mirror and prime launch failures use their retained source
to fence the original physical attempt. Check the original instance tuple and
refuse any physical permit with a durable serving resume before committing the
fence. Resume issuance and this check share the app quota journal lock. Orphan
sources remain usable without fabricating instance rows. Explicit physical
retirement retains its separate API and original source requirements.

Native uncertainty retains active instance state, host-port leases and local
resident accounting. No generic Destroy or terminal row can replace accepted
native stop evidence. Cleanup uses a detached bounded context (twice the native
retirement attempt timeout, centralized in `pkg/api/limits.go`). First fences and
accepted signed stops remain immutable. A worker prime whose handoff committed
is preserved when readiness or shutdown replies are uncertain.

Only after accepted original native absence may the scheduler conditionally
release its local reservation. The reservation carries the original run and must
match app, deployment, node and actual instance key. An unbound or replacement
reservation is retained. The instance completion writer validates the immutable
source and accepted stop, compares the original app/deployment/node/run and
expected state, and commits terminal state and host-port lease deletion together.
Terminal timestamps survive retries. Snapshotting uses its existing stopped
edge; other eligible states use their existing failed edge.

Retirement recovery also selects released sources with accepted stop evidence
while their exact original instance row remains resident. Engine-owned recovery
completes scheduler effects from those receipts without another native RPC.
Database failure advances the composite cursor and retries on the next traversal.
Already completed or replacement rows do not remain perpetual candidates.

All admission heads remain frozen. Full inventory, conservative restart RAM
accounting, native capture/pause/migration, first-fence reconciliation, directory
authority, global billing and routing/acceptance in all selected healthy regions
remain required for the end-to-end feature. This change covers captured initial
launch failure paths; it does not claim all VM lifecycle paths are integrated.

## Validation

Exercise both stores for exact tuple/state comparisons, first-fence retries,
resume refusal, accepted-stop-only row and lease completion and durable retry
selection. Check scheduler callers refuse generic teardown without an owner,
local reservations reject stale/unbound origins, and actual mTLS recovery handles
lost native replies and database completion errors. Run changed package race
tests, PostgreSQL contracts, sqlc consistency, lint and policy checks. Native
Linux x86_64 KVM test-metal, leakcheck and recovery acceptance remain required.
