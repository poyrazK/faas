# ADR-489: Transactional operation handler SDK

- Status: Accepted for implementation; runtime promotion follows ADR-488 qualification
- Date: 2026-10-03
- Related: ADR-430 (Commit), ADR-487 (customer routing), ADR-488 (webhook effects)

## Context

An operation can commit application PostgreSQL writes and lose its HTTP response
before Gregale accepts completion. Handler retry must preserve both the committed
business outcome and the original effect requests. The current consumer recipe
leaves this receipt storage, concurrency, and response reconstruction to each app.

## Decision

Ship a customer PostgreSQL transaction wrapper in the Node, Go, and Python SDKs.
Python supports synchronous and asynchronous psycopg connections. The wrapper
owns a fresh READ COMMITTED transaction. Its callback performs business writes
through the supplied transaction/cursor and returns a result and optional named
webhook effects. It does not commit, roll back, or perform external side effects.

The owner explicitly installs `public.gregale_operation_inbox`; SDK exports
package identical DDL. This is application-owned SQL, outside Gregale's control
plane and sqlc catalog, just like the existing customer Commit outbox. There is
no new platform table, daemon, or API. The shared schema and limits are generated
into each SDK from `pkg/operationinbox/schema.sql` and `pkg/api/limits.go`.

Obtain the request context through the SDK header factory, after Gregale ingress
has stripped untrusted values and authored the managed-operation headers. Require
result protocol version 1, positive int64 generation, and nonzero UUIDs for the
operation, account, app, and optional customer. The factory is not authentication
for arbitrary HTTP servers. Callback code still owns business authorization.

Fingerprint the exact method, raw request target (including query string), and
body with SHA-256 over `gregale-operation-request-v1\nMETHOD\nTARGET\nBODY`.
Methods are uppercase letters and targets exclude LF, CR and NUL, so framing is
unambiguous. Enforce the existing request and identity limits. Store account,
app and optional customer separately. Generation, deployment and volatile runtime
headers are deliberately excluded: a retry must recover the same committed work.
Business headers that affect meaning must be represented in the durable input.

Acquire `pg_advisory_xact_lock(hashtextextended('gregale.operation-inbox.v1:' ||
operation_id::uuid::text, 0))` before looking up the receipt. Every SDK uses the
same lock namespace and UUID primary key. Hash collisions only serialize unrelated
work. Read the receipt in a subsequent statement at READ COMMITTED so a waiter
sees the previous transaction's commit. Verify scope and digest before returning
any stored response. Mismatches fail without executing the callback.

On a first attempt, serialize and validate the complete version 1 response
envelope, insert it with the business writes, and commit once. Store JSON as text
to preserve exact response bytes, numeric precision, original effect intent, and
Unicode across SDKs. Responses and effect payloads follow ADR-488 limits. A
rollback removes both the business write and receipt. A process death releases
the transaction lock; no processing lease or incomplete receipt needs recovery.

Return exact saved JSON bytes on replay, without running the business callback.
Node and Go acquire a dedicated connection/transaction; Python requires an
exclusively leased idle autocommit connection so its transaction manager cannot
silently create a savepoint inside an outer transaction. Preserve a Python
connection's row factory for callback queries; receipt reads use tuple rows.
Commit acknowledgement errors are reported as outcome unknown. There is no
automatic callback retry. A subsequent attempt with the same identity safely
checks the receipt before making another mutation.

## Consequences

Applications can remove deduplication tables and saved-response reconstruction
code for managed PostgreSQL operations. Callback execution can repeat after a
rollback, but one committed receipt permits one committed set of business writes.
This guarantee assumes the callback uses only the supplied transaction and does
not control its lifecycle. Use normal business constraints and row locks for
invariants that require more than READ COMMITTED isolation.

Customer PostgreSQL and Gregale remain two separate transactions. A saved
response can be replayed until the current owner accepts it, but tenant suspension
or a revoked receiver may still prevent platform completion after business commit.
Saved effect requests are immutable; replay does not silently select another
receiver. Keep the existing platform fencing and delivery scope checks.
External API calls require their own idempotency contract. An intentionally new
operation ID represents new work; business-key deduplication remains application
logic. Retain receipts while their original operations can be replayed. There is
no automatic TTL, schema installation, or schema downgrade.

## Validation

Real PostgreSQL acceptance covers callback/validation rollback, eight concurrent
duplicates, generation changes, input/account/app/customer conflicts, an isolated
search-path decoy, ambiguous COMMIT recovery, and Python sync/async transactions.
A real Node HTTP handler is killed before commit and after commit before replying;
restart must recover one committed mutation and the original webhook intent.
Each SDK writes a receipt that all three SDKs replay in a later generation with
identical bytes, including numbers outside JavaScript's exact integer range.
Both customer and account scope are covered; payload byte validation preserves
original JSON number representations rather than re-encoding parsed floats.
Skipped PostgreSQL tests fail the combined acceptance script. These portable
tests do not establish native VM or production readiness.
