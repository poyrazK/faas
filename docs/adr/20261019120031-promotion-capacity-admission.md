# ADR-20261019120031: Durable capacity admission for verified promotions

## Status

Implemented behind the existing default-off promotion health gate. Native overload
qualification is pending; this decision does not enable production admission.

## Context

ADR-20261019120029 bounds short reconciliation claims, but an accepted promotion can keep
preparing workloads while many other operations remain active. Those claim limits
neither bound whole-operation demand nor give excess requests a useful waiting
state. Starting a verification deadline before capacity exists wastes its budget.

## Decision

Verified promotion admission allows 24 active operations fleet-wide and eight per
account. Excess requests enter a durable queue bounded at 128 fleet-wide and 32
per account, for at most 15 minutes. Limits live in `pkg/api/limits.go`. The four
fleet/two account reconciliation claims remain independent. Neither limit grants
VM concurrency, memory, builder slots, or extra application quota.

A queued promotion remains `status=running` and exposes `health.phase=queued`.
Its admission receipt preserves queue entry, wait deadline, dispatch time and
reason. Before dispatch, health timestamps describe waiting. Dispatch starts the
normal full verification or migration window once; active work never requeues.
Timeout fails the accepted operation without preparing workloads. Queue rejection
returns 429 `project_promotion_queue_full`, Retry-After and limit evidence. It
creates no operation and consumes no protected-environment approval. Approval
consumption commits atomically with acceptance, including immediate admission.
Existing idempotency keys replay the accepted operation even when the queue fills.

Postgres creation and dispatch serialize on a transaction advisory lock, in project,
admission, promotion order. READ COMMITTED counts use a fresh snapshot after lock
acquisition. Memory storage uses its existing mutex. Waiting is FIFO within each
account; another account can use available capacity. The durable fair dispatcher
continues to schedule active, queued and urgent jobs. Restoration and migration
recovery bypass admission; they still use existing reconciliation leases and count
against new starts. Previously active operations are grandfathered.

Dispatch requires the current unexpired health claim and exact generation. Under
input locks it checks both selected graph identities, sufficient retention,
qualification, captured configuration and versions, variables, secret fingerprints,
workload settings, bindings, flags and referenced customers. Changed intent ends
as conflicted and requires a fresh preview/approval. Validation makes no candidates,
tasks, holds or external calls. Preparation rejects queued owners. Queue abort,
including a migration that has not started, finishes immediately even after a
concurrent destination edit. Active migration cancellation remains unavailable.

A database trigger preserves admission history and permits queued activation only
with the validated dispatch transaction's lease/generation authorization. Old
writers cannot drop the receipt, requeue active work or skip directly to cutover.
Downgrade refuses any admission receipt or live claim rather than discarding
history or fencing. Deploy updated control-plane writers together before admitting
queued requests; retain the default-off production gate until qualified.

## Validation and limits

Portable contracts cover account and fleet admission, concurrent stores, bounded
waiting, FIFO, restart, stale ownership, full fresh health windows, expiration,
input drift, cancellation, approval rejection/retry and database transition fences.
API, CLI and SDK contracts expose waiting explicitly. These are control-plane
checks, not evidence of native VM overload behavior. Designated native KVM load,
rollback progress under saturation and final host leakcheck remain required before
production enablement. The ADR-20261019120030 load cohort stays below these admission caps
and does not qualify this queue by itself.
