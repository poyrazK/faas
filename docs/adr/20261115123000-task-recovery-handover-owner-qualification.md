# ADR: Task recovery handover and separate owner qualification

Date: 2026-11-15
Status: Accepted

## Context

The local Task executor has single-use owner authorization and monotonic native
checkpoints. Recovery inspection can describe interrupted releases, but a guard
before an HTTP write cannot fence a request already admitted. A migration can
also remain active outside the native API. A read-only plan is insufficient
recovery authority.

## Decision

Introduce a versioned trusted-executor fence. Its private claim accompanies all
native mutation requests except journal transitions. An account-authorized
middleware holds the original intent row lock until the synchronous mutation
handler returns; owner stop and executor exit acquire that same lock. PostgreSQL
uses the existing SQLC row lock in a separate transaction, without holding app
locks needed by the mutation handler. Journal acquisition uses SQLC NOWAIT locks
and returns the connection before retrying a busy row. At most half of each
store pool may hold admitted request barriers, leaving connections for the
actual writes. Pools with fewer than two connections fail closed. Memory uses
an equivalent per-intent gate.
Normal authorization, deployment review, traffic CAS and generation fences still
apply. The barrier serializes native intent writes, not asynchronous builder or
scheduler completion; recovery independently observes their results.

The owner requests an immutable handover bound to the operation and checkpoint
revision. Stop permanently rejects subsequent executor guards, checkpoints,
completion and admitted writes. Previously admitted writes drain before stop
commits. Settlement additionally requires a private claim-holder exit receipt.
The trusted CLI emits that receipt only after a normal gate exit, awaited children
and closed SQL pools. Timeouts, signals, malformed output, missing receipts and
legacy unfenced claims cannot establish settlement. Stop does not kill a process,
infer its absence from an expired lease, retry a submission or transfer its claim.
An executor that already exited normally can settle immediately. A lost exit
receipt can be resubmitted with the original private claim without renewing it.

The next step is owner-only qualification for restoring the captured web release.
The CLI checks immutable sources and installed dependencies, private role bindings,
Task compatibility/admission under the existing release advisory lock, previous
worker generations and readiness, observer freshness, the exact old artifact's
endpoint/catalog/roles, and a fresh native recovery report. It records an owner
attestation, exact target/current deployments and fresh native app snapshots.
Native storage locks and compares those snapshots at qualification and approval.
Local checks remain owner attestations, not server-observed database proof.

A separate explicit owner decision binds the full qualification digest, original
operation, checkpoint, restore target, expected traffic and a fixed one-minute
observation deadline. The original release review may already have expired or
been revoked; recovery uses current owner authority, never revives that review,
and cannot renew a qualification. Different or concurrent qualifications cannot
replace the first one. Revocation remains possible after expiry or app drift.
Recovery records never set the root readiness or binding-verification flags.

## Consequences

This milestone exposes no recovery apply tool or endpoint. One-time guarded web
restoration is a subsequent milestone and must requalify live conditions at the
actual traffic write. Resume and worker retirement remain separate work.

A crashed executor without an exit acknowledgement remains blocked. An operator
needs stronger process/SQL reconciliation before a future milestone can release
that block. Independent owner or agent API writes remain subject to their own
permissions and invalidate fresh generation/traffic evidence; the fence does not
revoke an account's general deployment permission. Fences apply to cooperating
versioned executors; old claims cannot opt into recovery after execution began.
No migration rollback, deployment, worker wake, traffic mutation or live staging
acceptance is performed by qualification.
