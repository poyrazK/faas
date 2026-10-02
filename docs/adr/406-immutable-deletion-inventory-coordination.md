# ADR-406: Coordinate immutable deletion with version inventory

Status: Accepted (2026-10-03)

## Context

ADR-404 gives permanent deletion a stable, owned immutable selector. Its retry
cannot remove a different version, but deletion can remove a continuation
identity while an ADR-398 inventory or ADR-405 preparation is paginating.
Decreasing physical usage alone does not prove a scan remains complete. A
lost provider response also leaves the timing of the first removal uncertain.

## Decision

Use the existing durable deletion journal for immutable selected versions too.
Bind the public account/bucket/key-owned reference to its private native target
atomically during admission. The target cannot change after insertion. Reject
admission while an inventory, configuration change or another deletion is
active. Admitted tracked writes and multipart sessions must drain first.
Unsafe legacy grants, missing versioning cutover, stale usage and exhausted
customer safety budgets do not prevent immutable cleanup. It creates no marker
and needs no storage reservation. Its acknowledgment never refunds capacity.

The shared bucket/account admission order serializes inventory creation and
deletion. A database trigger also verifies the exact reference and excludes
active inventory/configuration intents for immutable journal inserts. Every
customer S3 single/bulk, control DELETE and receipt POST path uses the journal;
the provider adapter remains a low-level one-attempt capability.

Before dispatch, persist the attempt phase. A live lease excludes stale workers;
lease expiry never releases a dispatched fence. Recovery may issue another
bounded request only for the persisted immutable target. A valid 204 or explicit
NoSuchVersion acknowledgment settles it. Transport failure, malformed response,
missing placement and timeout remain pending. A parsed initial AccessDenied/403
can fail the receipt because that attempt did not mutate. A rejection of a
recovery request cannot settle an earlier uncertain dispatch: it retains the
fence and retry schedule until positive acknowledgment. A sticky private recovery
claim flag enforces this rule in memory, PostgreSQL and the database trigger.
Prepared abandoned intents can still be cancelled before dispatch.

Once immutable deletion is fenced too, ordinary Enabled preparation may collect
its complete baseline through at most eight pages and 4096 entries. No owned
write or deletion can remove its continuation identity during that scan. The
same limits bound marker recovery. Oversized, malformed or incomplete scans fail
before mutation. Provider-side external writers remain outside ADR-398's trust
contract. Mutable null, Suspended ordinary and unversioned acknowledgment loss
still require stronger unique proof and remain open in the implementation ledger.

## Customer surfaces

POST objects/deletions accepts an owned public version UUID as well as null or
an omitted selector. GET retains progress and the acknowledged marker flag.
Go/Node/Python models and `gregale bucket deletions start ... [version-id|null]`
expose the same identity. S3 selected DELETE returns X-Gregale-Delete-Id too, and
signed retry IDs and positional bulk receipt IDs apply to every selector.
Replaying a completed receipt returns its stable result without contacting the
provider; a pending receipt requests no additional dispatch. The worker owns
recovery even when customer storage ingress is disabled. A distinct request
after physical removal can acknowledge a false marker flag.

## Operations and rollback

Coordinate this rollout across storage ingress workers and drain older workers
that can dispatch ADR-404 deletes outside the journal. Database fences cannot
serialize a provider mutation made by obsolete code without a durable intent.
Keep provider placement/credentials available for unresolved immutable attempts;
configuration failures retain the fence and are durably deferred. The additive
migration preserves ordinary/null receipts and refuses rollback while any
immutable intent or receipt exists, including terminal receipts.

## Acceptance

Local memory/PostgreSQL tests passed for deletion versus inventory admission,
paused page cursors, reconstructed stores, exact ownership, immutable target
protection, receipt replay and conservative capacity. SDK/SigV4/provider HTTP
tests and control clients passed for lost responses, restart recovery, rejection
of a later retry, disabled ingress, invalid selectors and paginated baselines.
Scoped race suites, migration round-trip and rollback guards passed. Go route
coverage, five Node tests, five Python tests, scoped Python lint, SQLC parity,
Node generation determinism and scoped Python generation parity passed.
OpenAPI validation reported zero errors; changed-code Go lint reported zero
issues. Repository encoding, shell quoting, ADR uniqueness and runbook SQL
checks passed. No real provider qualification is requested.

AWS protocol references: [DeleteObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObject.html),
[ListObjectVersions](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectVersions.html).
