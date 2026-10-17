# ADR: Python async deferred event acknowledgment worker

- Status: Accepted
- Date: 2026-10-17

## Context

Python has application PostgreSQL consumer receipts and generated acknowledgment
endpoints, but no worker coordinating their lease and recovery boundaries. A
callback must confirm the exact attempt before business processing, while saved
receipt JSON must survive completion retries without numeric or text changes.

## Decision

Add `AsyncEventAcknowledgmentWorker.from_headers` for authenticated, durably
retained original deliveries and an app-authorized generated `AuthenticatedClient`.
Capture immutable receipt identity, attempt/generation, token and bounded callback
settings. Own one event loop and reject overlapping operations. Redact the worker's
string representation and reject copying/pickling of the capability.

`run` probes the existing receipt before any new business work. Missing receipts
require confirmed renewal of the exact token/attempt/generation; a scoped delivering
status permits only a bounded handoff retry, never admission by GET alone. Renew
serially while invoking the existing async psycopg receipt transaction. Stop/join
renewal before posting exact committed result bytes. `complete_receipt` never calls
business code. Explicit retry/dead-letter decisions check for receipts first and
require stopped work, known rollback and exclusive durable job ownership.

Keep the public receipt helper signature intact. Its async implementation shares
private optional checkpoints with the worker around driver/handler awaits and
immediately before transaction completion. Shield the worker operation from caller
task cancellation; signal a read-only cancellation view and join active SQL and
cleanup. Do not force-cancel business work or driver operations. Once COMMIT may
have been sent, preserve unknown outcome errors; confirmed receipts remain confirmed
after cancellation/lease loss. Native cancellation remains a CancelledError subtype
with receipt progress attached.

Discard the exclusively leased connection after an unknown COMMIT and preserve
that original error through close cleanup. Reconciliation uses a fresh connection.

Use the configured SDK HTTPX client with redirects disabled, bounded callback
deadlines, a 64 KiB response bound, fixed status validation and native integer
fencing. Copy only known observation fields. Serialize completion by embedding
saved JSON bytes, and retry only unchanged callback inputs. Honor valid bounded
Retry-After hints from raw responses. Wrapper errors that omit those hints stop
helper retries. Callback errors expose fixed codes/status/progress, not remote
bodies, HTTP requests or raw transport causes.

## Consequences

The application owns authenticated ingress, encrypted/restricted durable records,
exclusive job ownership, an idle autocommit connection, transaction-only handlers,
driver timeouts and protected callback recovery. An example keeps its ingestion
job until terminal callback confirmation; its mode is explicit and immutable.
No worker/loop/server starts automatically and no schema is installed or pruned.

This adds immediate callback coordination using the existing receipt DDL and API.
Durable intent storage, local preparation leases and preparation metrics remain
separate increments. Receipt-absence observations grant no ownership, lease expiry
does not stop external effects, and external effects still need an outbox or
idempotency protocol. No platform schema, route, migration or API registry changes.
See the [worker and cancellation contract](../event-acknowledgment-python-worker.md).

## Validation

Use Python compilation/lint, generation parity in an isolated tree without test
files, wheel/sdist package inclusion, schema parity and documentation/static
checks. No tests/assertions are added or changed and no local tests are run.
Hosted database/callback/cancellation qualification remains required before release.
