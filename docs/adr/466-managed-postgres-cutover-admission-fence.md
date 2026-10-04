# ADR-466: Durable admission fencing for managed PostgreSQL cutovers

Date: 2026-10-01

Status: Accepted; customer activation remains disabled

## Context

ADR-464 stages restore credentials and ADR-465 verifies their SQL identity and
privileges. Neither moves workloads. A subsequent drain must prevent new VM
admissions while the scheduler retires existing writers. Runtime configuration
rolling restarts start replacements before retiring predecessors; maintenance
mode covers public ingress, leaving scheduler work admission available. Neither
provides the cutover barrier required by spec §6 and §11.

## Decision

Persist an app-wide admission fence in `apps`, recording its cutover ID and the
database clock timestamp. The control plane installs this barrier under the
account, app, then intent locks, after every staged credential has fresh
verification evidence. Validate freshness again in the database after lock waits.
Only one intent can own an app's fence, even when other scopes have staged intents.

The fence is internal controller plumbing. Prepare and Verify do not acquire it.
There is no customer activation or fence endpoint in this slice. The PostgreSQL
store is its only production acquisition adapter; MemStore reports no fence.

The scheduler checks the barrier before its running-instance Wake shortcut and
the shared admission pipeline, including paths that bypass normal request gates.
The PostgreSQL instance trigger provides the authoritative concurrency barrier:
new resident instance rows and transitions into waking, cold booting, running,
or warm cannot pass after acquisition. Builder instances without an app and
terminal history rows retain their existing behavior. Cleanup transitions remain
available. Telemetry updates that keep the same app and state remain available.

The trigger holds an app SHARE lock. Acquiring the fence updates the app tuple,
so a competing read-committed transaction sees the new barrier after waiting and
a transaction with an older repeatable-read snapshot must abort. Merely locking
the app without changing its tuple would not protect the latter case.

The fence has no TTL and remains present through restart, retry, verification
refresh, expired verification, and partial cancellation. Expired evidence cannot
reopen admission. Cancellation releases it atomically with the final recorded
credential revocation and binding/database unpinning. Cancellation reconciliation
uses the acquisition lock order, preventing app/intent lock inversion. Rollback
refuses to discard a live fence.

Credential sealing and revocation writes recheck the lease against PostgreSQL's
clock after lock waits. A caller's earlier timestamp cannot prolong its authority
or release the fence after expiry; a replacement worker can safely retry.

## Limits and follow-up

An admission fence is **not writer-drain evidence**. Existing VMs, cached gateway
routes, running workers and jobs, external SQL connections, and other apps using
the source database can still execute. A VM admitted before the barrier may be
inside a vmmd RPC; its running publication will fail, but destruction still needs
confirmation. The fence's timestamp cannot authorize binding publication.

The next slice must have the owning scheduler retire every relevant resident VM,
confirm vmmd destruction, keep route and work-dispatch barriers closed, and persist
an acknowledgment fenced against ownership changes and in-flight boots. Atomic
binding publication must require that acknowledgment and fresh staged credentials,
invalidate snapshots, then explicitly resume workloads. It also needs a clear
restore rollback policy: draining now does not copy writes made since the restored
point into the target. This feature must not imply those writes are preserved.

Validation covers acquisition idempotency, tenant isolation, stale/future member
evidence, old SQL transactions, warm promotion, worker/job/mirror admission,
verification refresh, cancellation, rollback, and scheduler catalog failures.
Spec §14 and CLAUDE.md additionally require native x86_64 Linux KVM `test-metal`
and `leakcheck` evidence before enabling this lifecycle behavior for customers.
