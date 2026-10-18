# ADR-202610180816: Bounded recovery for internal CI automation admission

- Status: implemented behind existing default-off CI gates; native qualification pending
- Date: 2026-10-17
- Extends: ADR-1656 and ADR-1577

## Decision

Add explicit operator recovery for an authenticated automatic push whose
admission exhausted its eight attempts because of capacity or availability.
Recovery resets admission of that same source event. It never reruns commands,
changes the pinned commit/profile, creates an execution directly, promotes a
metadata receipt, or revives ignored/revoked events.

Each event has at most three lifetime recoveries, bounded in
`pkg/api/limits.go`. Each recovery permits the existing eight-attempt admission
budget: at most 32 admission attempts including the initial budget. The existing
256 combined retained source events/account bound is unchanged, so the new audit
journal is bounded to 768 rows/account. Event and retry evidence are permanent
within the account lifecycle; archival does not prune or reset them.

## Transaction, identity and authority

apid owns both automatic admission and its recovery. Under the account lock it
checks active Jobs-capable account authority and binds the request to the exact
account, binding and event. The caller supplies a stable idempotency key and the
current `expected_retry_generation`. Only the key's SHA-256 digest is retained.
The same key/event/generation returns its original receipt, including after
worker progress or a lost response. A reused key with different identity or a
stale generation conflicts; a different key cannot reset pending admission.
Replaying a receipt makes no state change, including after policy revocation.

New recovery requires a dead event with eight exhausted attempts, an availability
or capacity classification, no lease, and no admitted execution. The original
`github-auto:<event UUID>` execution key must also remain unused, including by
an archived execution or explicit manual submission. The current policy UUID
and generation, binding generation and branch filters must match capture.
The profile must remain active with the same materialized template/image
authority. A fresh repository-access RPC runs before the transaction; local
account, binding, profile and image authority are checked again under the lock.
Provider details are replaced by stable public error classifications.

The permanent receipt captures the prior attempt count/error and requested
generation/time. Receipt insertion and resetting the source to pending, with
zero attempts and no old lease token, commit atomically. A reset or audit failure
rolls back both. Row-lock contention returns a conflict without waiting for a
worker's event lock. A stale worker cannot admit or overwrite a recovered event.

The existing worker performs admission later with the original immutable source
snapshot and execution key. It rechecks every normal gate, source authority and
execution quota. Disable/replacement invalidates recovered pending work exactly
as it invalidates original pending work. No new worker or automatic redrive loop
is introduced.

## API, CLI and dormant defaults

New account-scoped routes:

- `GET /v1/internal/ci/github/automation/{binding}/events/{id}`
- `POST /v1/internal/ci/github/automation/{binding}/events/{id}/retry`

POST accepts only `expected_retry_generation` and requires `Idempotency-Key`.
It returns 202 with an audit receipt; a response replay sets
`Idempotency-Replayed: true`. It requires internal/GitHub/automation opt-in,
the allowlist, admin scope, MFA and trusted HTTPS. Worker/dispatch/transport
can remain off: recovery records intent without admitting compute. Reads remain
available with GitHub/automation/admission disabled, while internal account
policy, admin scope, MFA and HTTPS remain mandatory.

Event history and status report retry generation, maximum recoveries, last
receipt, next attempt/lease expiry and stored-state eligibility. `retryable`
does not establish current source permission, worker readiness or native
qualification. Responses expose no commands, private source/image paths,
provider responses, lease tokens or idempotency digests.

`gregalectl ci automation status` and `retry` require explicit `--binding` and
`--event`. Retry additionally requires `--generation` and `--key`. The normal
audit stream records a new request once; the transactional receipt remains
durable evidence if the audit stream or HTTP response is lost.

Every existing production CI flag remains off. The account allowlist remains
empty. Public compute/dispatch availability and the frozen qualification bundle
are unchanged. The additive migration is replay-safe; rollback refuses to drop
any recovery receipt. Roll back readers before removing a fresh unused schema.
The retry journal is registered as operational clone inventory and is never
copied as project configuration.

## Portable verification

PostgreSQL/race coverage exercises response loss, concurrent requests, lifetime
bounds, stale generations, request-key conflicts, revoked/replaced source and
image authority, account/binding isolation, metadata exclusion, admitted and
occupied execution keys, old leases, quota enforcement, rollback safety and
atomic audit/reset failure. HTTP/CLI coverage checks validation, redaction,
idempotent responses, dormant compute and the existing admin/MFA/HTTPS boundary.
Migration checks cover fresh apply, schema/source binding, SQLC drift and replay
with unchanged constraint fingerprints. No real provider, command execution or
VM lifecycle acceptance occurs in the cloud workspace.
