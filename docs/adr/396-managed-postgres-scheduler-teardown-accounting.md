# ADR-396: Retain scheduler admission until confirmed teardown

Date: 2026-10-02

Status: Accepted; customer cutover activation remains disabled

## Context

ADR-395 makes vmmd retain ownership when process exit or resource cleanup is
unconfirmed. schedd can still defeat that guarantee by freeing admission before
the Destroy RPC succeeds. Watchdog, liveness and workload OOM paths previously
continued to publish their completion outcome after a failed Destroy. Read errors
and state races also released reservations, including when a wake had completed
or a different lifecycle operation still held a resident guest. This violates
spec §6.2's capacity accounting and prevents reliable managed PostgreSQL drains.

## Decision

Watchdog, liveness, workload OOM, operator ForceRestart, disk-pressure recycling
and egress-abuse recycling release their scheduler reservation only after vmmd
confirms Destroy. An error leaves the original RAM-counting state and reservation
intact and propagates with operation context. Retain RAM, guest vCPU topology,
host CPU quota, existing concurrency contribution and host-port leases. Do not
publish successful teardown, advance restart metrics or budgets, stamp a deployment
failure, schedule a replacement or escalate egress abuse on a failed stop.

A failed InstanceByID read, including ErrNotFound, is not teardown evidence.
Propagate it without releasing admission. A state mismatch leaves reservation
ownership with the operation that moved the row. ForceRestart preserves its
existing ErrInstanceNotRunning response; other mismatches remain no-ops. Existing
snapshot invalidation before Destroy is preserved, including the operator's
partial-success snapshot ID response.

Keep the failed-stop state durable instead of using an in-memory cleanup-only
marker or publishing a terminal row. SeedLedger can then reconstruct the same
resident reservation after schedd restarts. Rebuild its deployment ID as well:
losing that identity previously made the same deployment appear to be a new
rollout revision and incorrectly allowed the extra overlap slot. The existing
SNAPSHOTTING accounting
continues to hold RAM/CPU while dropping serving concurrency. A successful stop
uses the existing state-machine outcome, host-port release and reconciliation.
The watchdog's WAKING→COLD_BOOTING fallback is unchanged.

## Limits and acceptance

This slice covers the six named Engine operations. It does not make every
scheduler terminal row a drain receipt. Wake/Prime error tails, migration cleanup,
account deletion and divergence recovery still need an ownership audit before a
fleet drain controller can rely on their state. A missing durable row can retain
in-memory capacity until ownership reconciliation; it cannot be treated as proof
that the guest is gone. SeedLedger recovery requires intact resident rows and
the existing app/node ownership assumptions. vmmd crash recovery is still pending.

RUNNING remains RUNNING on failure; this is capacity retention, not routing or
SQL-session fencing. Existing traffic can still reach that instance. vmmd's
liveness and OOM relays do not durably retry failed reports: reliable report
redelivery or a durable drain reconciler must drive retries. Operator callers can
retry and watchdog rows remain eligible for the next sweep. Successful state
writes retain their existing best-effort behavior and are not durable stop
receipts. Customer cutover activation stays disabled.

Portable tests hold Destroy in flight, inject failure, check retained admission
and host ports, rebuild SeedLedger, reject replacement capacity, then retry and
verify release. Read-error and resident-state-race cases cover each operation.
The real-guest metal regression injects an unconfirmed scheduler→vmmd stop for
liveness, OOM, operator restart and cold-boot watchdog; it checks the running
Firecracker PID/network, scheduler restart accounting, and confirmed retry with
leakcheck. Its store is in memory; database-backed/fleet crash recovery remains a
separate gate. Native x86_64 Linux KVM acceptance is required by CLAUDE.md and
spec §14; nested internal-node diagnostics cannot replace that release gate.

[Internal KVM diagnostic evidence](../ops/evidence/20261002-managed-postgres-scheduler-accounting/README.md)
records 19 passing top-level checks (82 including subtests), no selected skips or
failures and three passing in-run leak checks. Supported native acceptance remains
pending; the extra host-wide check could not acquire the shared acceptance lock.

[ADR-397](397-managed-postgres-failure-report-redelivery.md) adds persistent
redelivery for liveness/OOM reports, with an explicit boundary for unknown guest
ownership after vmmd restart. The all-node drain and crash-inventory gaps remain.
