# ADR-623: Atomic regional launch issuance

Date: 2026-10-05
Status: Initial launch issuance integrated; admission activation pending.

## Context

A scheduler retry must reuse the exact physical attempt and initial serving
seat. Separate reserve and issue calls can leave partially issued bookkeeping
when concurrency admission fails. A receipt identity must not change merely
because a retry changes its grant, deployment, purpose or rollout claim.

## Decision

Add mandatory state-store placement issuance. Under the existing app journal
lock, check the full current signed grant and tenant binding, reserve physical
and optional initial serving receipts, and mark both issued atomically.
PostgreSQL uses existing SQLC statements in one READ COMMITTED transaction;
memory stages both mutations under its journal mutex. Failure rolls back new
holds and does not issue pre-existing planned holds. Exact retries preserve
original timestamps. Fenced, stopped or retired attempts cannot be reopened.

Derive initial launch IDs from a versioned namespace, cell/app, registered
node/host epoch, instance and run. Grant, deployment, purpose and rollout
claims remain immutable receipt fields but do not alter these IDs. The hash
is only an idempotency key. Every initial serving launch gets a seat; warm,
prime and task bookkeeping do not. This constructor cannot implement a warm
resume or mint another serving authorization for an existing attempt.

Wire scheduler cold boot, snapshot restore, prime and paused warm-pool restore
through launch preparation. Resolve the registered compute node and discover
its exact host journal epoch, issue local receipts, check the coherent issued
placement, then recheck quota admission before dispatch. No directory is read
on this path. Issued capacity is retained on RPC errors, instance collection,
or best-effort cleanup; none proves physical stop. Independent apps require
no host epoch discovery. Store decorators retain the mandatory writer.

All accepted quota heads remain frozen before issuance on production paths,
including a head that arrives during preparation. Issuance is independently
testable bookkeeping and cannot activate a quota head. The initial adapter
uses steady seats only; rollout selection still requires its own scheduler
integration. Task dispatch needs durable lease/attempt/run pins before using
the constructor. Warm promotion and migration rollback require fresh-seat
transport and irreversible closure of the previous authorization. They keep
their existing frozen-admission guards.

Before activation, also complete signed compute admission, durable native
resource identity, startup inventory, pause/teardown/recovery verification and
stop emission, explicit quota consent, global billing, deployment orchestration,
and ingress to all selected healthy eligible regions without a directory
request-path dependency. Dedicated native Linux x86_64 KVM test-metal,
leakcheck and recovery acceptance remain required. Shared nested GCE unit and
private PostgreSQL checks do not supply this evidence.

## Validation

Run memory and real PostgreSQL contracts for concurrent retries, conservation,
rollback on seat exhaustion, planned holds, changed immutable claims,
fences, retirement and cancellation. Check stable initial IDs and new
incarnation IDs. Exercise scheduler registered-node discovery, missing or
cross-node epochs, immutable pins, storage failures, retained issued holds,
independent launch compatibility and frozen dispatch. Run affected race
regressions, diff lint and repository policy checks.
