# ADR-397: Persist and redeliver vmmd failure reports

Date: 2026-10-02

Status: Accepted; supported native acceptance pending, customer cutover disabled

## Context

ADR-396 retains scheduler capacity when vmmd cannot confirm teardown. Liveness
loops and guest workload OOM listeners emit their failure once, then exit. A
failed RPC therefore leaves a retained RUNNING instance without an automatic
retry. Cross-node placement also acknowledges a transient `pg_notify` relay
before the app's owning schedd has applied it. Notifications are hints, not a
durable delivery or cleanup receipt (spec §6.1–6.2, ADR-078/121).

## Decision

vmmd owns a node-local persistent failure outbox at
`/var/lib/faas/vmmd-failures`, provisioned root-only by systemd's StateDirectory.
`failure_report_dir` can override the path; the operator must provision a
persistent private directory and permit writes through the service sandbox.
Do not put the spool in `/run`, a Firecracker jail or a disposable cache.

Both Manager sinks synchronously write a versioned, bounded JSON record to a
0600 temporary file, fsync it, atomically rename it to its hashed instance/kind
key and fsync the directory. Bind each report to the resolved source node.
Default-local vmmd resolves the durable compute row; configured delivery without
a node ID fails startup. DB-less development can omit the schedd target.
Keep the first payload of each kind, including OOM
peak/plan values and liveness reason. Duplicate observations coalesce. A local
storage error is returned/logged and retained in memory for persistence retry;
crash durability in that case requires storage to recover before daemon exit.
Local persistence can block the receiver briefly; schedd RPC latency cannot.

A single exclusive spool lock prevents competing writers. Corrupt, oversized,
nonprivate, unsupported-version or unexpected entries fail startup. Incomplete
temporary writes are discarded, never mistaken for committed observations.
There is no time-based expiry, retry limit or error classification that drops a
report. Missing rows and denied/unsupported RPCs remain visible and pending.

Four delivery workers run under the daemon context, independently of the probe
or framework receiver. Attempts have a 20-second deadline; retries back off
from one second to one minute with per-instance jitter. Shutdown cancels and
joins workers while retaining committed reports. Network calls run outside the
spool lock. Logs expose pending attempts, delay and successful acknowledgement.

Add `applied` to both failure acknowledgements (ADR-016); keep `ok` as the
legacy acceptance/no-op signal. Production schedd reads the durable instance
outcome and sets `applied` only for parked, stopped or failed. Resident states,
account-deletion markers, read failures and missing rows cannot retire a report.
Foreign hosted instances still relay through the app owner; vmmd retries the
notification until it observes an applied outcome. Row-based app/host ownership
guards remain in place. Live migration preserves instance IDs: carry the
source node through RPC and notification payloads, validate it in the Engine
lock-held reread, and refuse a stale report about a different current node.
Acknowledgement reads also validate source identity. Legacy unbound reports keep
their existing contract. Only schedd changes instance state or admission.

vmmd requires both `ok` and `applied` before removing a committed record and
syncing its deletion. Older schedds omit `applied`, so a mixed-version rollout
keeps reports pending. Older vmmds retain their previous one-shot behavior;
deploy both components for durable delivery. Resolver-less in-process test
adapters retain their previous acceptance contract; production always wires the
store, including single-node deployments.

## Recovery boundary and remaining work

Spool recovery is not guest-inventory recovery. On open, mark loaded reports as
recovered. Before delivering one, the production sender requires that this
Manager own its live, waking or retained-cleanup resource identity. A stop
serialization reservation for an unknown ID is insufficient. An empty
Manager after a vmmd crash cannot issue a report that causes schedd to treat
unknown local ownership as a successful stop. Once ownership is observed, keep
that permission for the Manager lifetime so a lost acknowledgement after cleanup
can be retried. The next daemon must reconcile ownership again. Until inventory
reconstruction exists, such recovered reports stay pending and require operator
ownership reconciliation; automatic crash cleanup is not implemented here.

An applied report is an observation outcome, not an all-node drain receipt.
Other terminal-writing paths, best-effort state/event writes, ownership epochs,
SQL-session/routing fences and cutover publication still need work. Node binding
refuses reports delayed past a completed move, but does not add transactional
fencing against concurrent migration or distinguish a return to the same node
under a new incarnation. Losing the
spool disk also loses its reports. RUNNING capacity remains held through failed
Destroy per ADR-396; customer cutover activation stays disabled.

Portable verification covers retries/coalescing, abrupt process-exit replay,
failed storage recovery, bounded/cancelable workers, corrupt/exclusive spools,
canceled producers, negative and legacy wire acknowledgements, unknown guest
ownership and hosted-owner application. The real-guest diagnostic retries
failed stops through the actual schedd RPC handler, reopens the spool with the
same Manager, confirms retained capacity and verifies successful cleanup with
leakcheck. It uses an in-memory store and cannot establish database/fleet crash
recovery. Native x86_64 Linux KVM remains the release acceptance requirement.

[Internal KVM diagnostic evidence](../ops/evidence/20261002-managed-postgres-failure-reports/README.md)
records 34 passing top-level checks (127 including subtests), no selected failures
or skips, and three passing in-run leak checks. The owned stage was cleaned up.
Supported native acceptance and customer cutover activation remain pending.
