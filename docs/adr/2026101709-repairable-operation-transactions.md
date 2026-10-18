# ADR-2026101709: Receipt-bound repairable operation transactions

- Status: accepted for internal operator qualification
- Date: 2026-10-17
- Extends: [selective repair](20261016-selective-data-repair.md) and [writer readiness](2026101708-repair-capture-readiness.md)
- Scope: optional application-database contract and SDK transaction composition. No control-plane schema, HTTP authority or VM lifecycle change.

## Decision

Add managed and Customer Operations repairable transaction entry points to Go,
Node and Python (sync and async). Each owns capture, the supplied business
callback and the existing receipt transaction. A private shared-engine guard runs
after receipt insertion or validated receipt lookup and before COMMIT. Existing
transaction/capture entry points and wire protocols retain their behavior.

Automatically begin and finish capture around the business callback. Require
an exact qualified enrollment name, trusted environment/release configuration
and the existing negotiated request context. Use the current operation ID as
the source capture ID. The wrapper does not authorize arbitrary incoming HTTP
headers, untrusted SQL clients or operations outside this API.

## Durable binding

Matching journal and receipt IDs alone do not establish that they committed
together. Add an owner-installed `gregale_repair.capture_receipts` table, an
immutable protection trigger and `capture_transaction(..., phase)` function with
begin/seal/replay phases. Include this optional contract in initial schema and
explicit repeatable owner upgrades. Never run DDL or grants at application
startup or request time. The application-owner SQL exception covers these fixed
SDK calls; control-plane queries continue to use sqlc.

The binding retains operation/protocol/scope, release, qualified table,
enrollment, schema fingerprint, transaction identity and SHA-256 request/response
digests. It stores no business values, request/response bodies or row payloads.
Use the existing context/table byte limits in `pkg/api/limits.go`. Writer access
adds only SELECT/INSERT on this table and EXECUTE on the phase function. Trigger
EXECUTE, plan, approval, apply and maintenance grants are unnecessary.

Before the callback, verify required access, origin replication mode and receipt
protection, lock the binding table against protection DDL, and invoke the existing
capture begin contract. After the callback, finalize capture. Before binding,
repeat the enrollment/fingerprint check. Inserting the binding verifies a
finalized original capture in this transaction and matching scope/release/table/
enrollment/fingerprint, plus the actual selected SDK receipt's owner, request and
exact response digest. Compare receipt xmin to the current transaction only
during this insertion; historical identity never depends on xmin or frozen XIDs.
Callback transaction/savepoint lifecycle control remains unsupported.

Protect bindings against UPDATE/DELETE, including ordinary owner commands.
Database owners remain trusted and must quiesce DDL for requalification, as for
the existing journal. Owner-installed functions are not a hostile-SQL sandbox.

## Replay and failures

On replay, validate the existing receipt normally, then compare its capture
binding against the original trusted configuration and exact response bytes.
Do not begin capture, run the callback, query business rows, refresh the schema
fingerprint or manufacture metadata for an older receipt. Missing binding is
explicitly missing; different scope/release/table/protocol/digests are mismatched.
Retain and supply the original capture configuration across deployment changes.

Replay uses the immutable binding and continues after journal payload compaction
or later business/schema changes. Keep bindings with existing indefinitely
retained source identities and SDK receipts. They do not pin payload retention,
reconstruct purged values, confer repair authority or attest universal coverage.

Any pre-COMMIT capture or binding failure rolls back business writes, journals and
receipts together. Zero-row operations are valid finalized captures. Stable
missing/mismatch/incomplete/unavailable errors redact guard SQL failures; callback
errors retain existing behavior. After a lost COMMIT response, use the existing
unknown outcome error and retry the original identity/configuration; the committed
receipt/binding determine replay without repeating the callback.

Base executor/capture readiness does not require this optional contract. Keep
these functions outside its compatibility/privilege manifest; new entry points
check their own prerequisites before the callback. Base readiness therefore
does not attest the additional grants for this API.

## Verification

Disposable PostgreSQL tests exercise both receipt protocols in all SDKs under
restricted writer roles, including Python sync/async. Verify finalized captures,
zero-row commands, exact replay, release/environment/table mismatches, absent
legacy bindings, callback failures and failures after receipt insertion. Inject
a lost real COMMIT acknowledgement and verify idempotent recovery. Subsequent
human edits remain conflicts in the ordinary planner.

Database contract tests reject unfinished captures and receipts from a different
transaction, enforce immutable bindings and qualify disabled protection. Replay
survives payload compaction and repeated owner upgrades. Existing receipt,
milestone/workflow, readiness, scope and repair acceptance tests remain required.
The CLI journey records its 500-product import through the new Python entry
point under a restricted writer role, restores 460 while preserving 40 human
edits, then replays the original import after payload purge without new writes.
