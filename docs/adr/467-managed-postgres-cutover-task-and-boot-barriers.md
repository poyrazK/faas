# ADR-467: Command and boot barriers for managed PostgreSQL cutovers

Date: 2026-10-01

Status: Accepted; customer activation remains disabled

## Context

ADR-466 fences ordinary instance admission. Deployment-attached release, manual
and command-cron tasks use `app_tasks` and disposable VMs instead of `instances`.
Release tasks can receive migration credentials (ADR-462). Their command dispatch
must participate in the same app-wide barrier before the scheduler can drain the
app for a restore cutover (spec §6 and §11).

Additionally, only job boots had a manager-side cancellation/join barrier.
Destroying an ordinary VM during artifact restoration could finish before Wake
registered it in the live map. Its later return could recreate a source writer.

## Decision

Protect entry into `app_tasks.restoring` and `app_tasks.running` with the same
PostgreSQL app SHARE lock and named admission error as ordinary instances. Fence
acquisition changes the app tuple, so old repeatable-read writers abort and
read-committed writers see the barrier after lock waits. Lease heartbeats,
cancellation and terminal cleanup remain available.

Claim queries skip queued work for fenced apps, preserving the queue subject to
its usual start deadlines and allowing other apps to dispatch. The trigger remains
authoritative for a concurrent fence or a direct writer. Use sqlc for the claim and
running queries; refresh the existing
task and schedule-occurrence schema declarations needed to generate them.

The scheduler checks admission before resolving task environments/capacity and
before priming a deployment. If a claimed task encounters a cutover fence during
restore or before its durable running transition, no command is dispatched. A
successfully restored VM is destroyed before recording `database_cutover_fenced`.
Failed destruction retains the existing recovery/lease path and prevents a
successful terminal acknowledgment. Already-running tasks still require draining;
this change does not cancel or replay their commands.

Generalize the existing job boot flight registry to every Manager Wake path:
cold boot, snapshot restore, warm-pool construction, app tasks, disposable execution,
builders and migration adoption. Register before resource acquisition, reject
duplicate instance boots, cancel and join from Destroy/SignalAndKill, and check
cancellation under the same lock as live publication. The join finishes after
boot error cleanup or live publication, including the post-publication hooks.
Timeouts return an error while retaining the boot flight for later cleanup/retry.
Registry entries are removed on completion; no per-instance tombstone accumulates.

## Limits and follow-up

Integration with ADR-393's exclusive operations preserves their operation ID,
generation, lease and attempt deadline through generated claim/dispatch rows.
Claims skip expired or replaced operation owners. Running transitions hold the
task and operation locks while applying the PostgreSQL admission trigger. An
expired first-start deadline cancels the task through that same transaction,
avoiding a second connection waiting on the task's own row lock.

These are prerequisites for a drain acknowledgment, not that acknowledgment.
The boot join covers calls that have entered the manager. It does not fence an
RPC still in transit that arrives after destruction, survive a vmmd restart as a
durable tombstone, serialize every concurrent teardown, or prove cleanup after a
failed kill. Admission and command barriers do not stop existing VM processes or
external SQL clients. Task lease expiry is not proof of VM destruction.

The next slice still needs a scheduler drain request and receipt, an authoritative
vmmd barrier against delayed creation/resume on every relevant node, confirmed
destruction of ordinary and task VMs, route/work-dispatch convergence, and durable
ownership fencing. Atomic binding publication must require that receipt, fresh
credential evidence, snapshot invalidation and explicit workload resume. Draining
does not recover source writes made after the restored point.

Tests cover late successful boot returns, stuck-boot teardown timeouts, duplicate
boots, task commands fenced after restore, failed destruction, queue preservation,
cross-app dispatch, manual/release/cron work and old PostgreSQL transactions.
Native x86_64 Linux KVM `test-metal` and `leakcheck` evidence remains required by
spec §14 and CLAUDE.md before declaring lifecycle acceptance complete.
