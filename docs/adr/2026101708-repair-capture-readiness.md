# ADR-2026101708: Application writer capture readiness

- Status: accepted for internal operator qualification
- Date: 2026-10-17
- Extends: [executor readiness](2026101707-repair-readiness.md)
- Scope: capture catalog probes, Go/Node/Python SDKs, local CLI and diagnostic profile identity. No owner/control-plane schema, approval or VM lifecycle change.

## Decision

Add a separate `capture` readiness profile that observes the application's
actual writer connection. Executor readiness continues to observe its configured
executor connection and check policy. A ready executor cannot establish writer
permissions, receipt compatibility or session settings.

Expose `CheckDataRepairCapture` in Go, `checkDataRepairCapture` in Node and
`check_data_repair_capture` / `acheck_data_repair_capture` in Python. Add local
`gregale data-repair doctor --profile capture --database-env NAME --scope-file FILE --table TABLE --release RELEASE`
with optional `--receipt-protocol managed|customer`. Default CLI/API/UI diagnostics
remain executor diagnostics. APIs accept no writer DSN, role, scope or profile
override; the capture CLI rejects hosted/API selectors. Distinct report types
carry `profile: capture|executor`; existing executor reports without the field
remain readable for rolling compatibility. Executor consumers reject capture
reports.

Trusted scope/release configuration supplies report context only. It does not
attest that arbitrary database contents belong to a platform account, app,
environment or customer. Customer receipt selection requires a configured
platform tenant UUID. Reports confer no repair or platform authority.

## Fixed shared probe

Generate a client-side probe from the core's embedded owner schema and catalog
queries. Embed identical SQL, finding messages and limits in the three SDKs;
the Python representation adapts only placeholder/percent syntax. The Go SDK
remains a standard-library leaf without a root-module dependency. No owner probe
function or schema upgrade is needed. A generator freshness check runs in the
portable repair acceptance gate.

Own a read-only, repeatable-read snapshot with a fixed catalog search path.
Inspect prerequisites before reading the exact enrollment metadata row. Use
trusted embedded catalog expressions for fingerprints and compare installed
function signatures, bodies and invocation properties without executing them.
Read no business, journal, plan or receipt payload rows. Execute no installed
capture/check functions, `EXPLAIN`, DDL, maintenance, rehearsal or repair actions.
The selective-repair ADR's application-owner SQL exception covers these fixed
catalog queries; control-plane SQL continues to use sqlc.

Return ten fixed findings: connection, PostgreSQL major version, capture
installation, function compatibility, receipt shape, session settings, writer
permissions, enrollment, fingerprint and triggers. Require the capture subset of
repair tables/functions and the selected SDK base receipt inbox. Check the four
runtime function EXECUTE grants actually used by capture: begin, finish,
check_enrollment and fingerprint. Trigger functions must be compatible but
PostgreSQL does not require writer EXECUTE privileges to fire them. Do not
require plan, approval, apply, retention or executor check-policy access.

Require schema usage, enrollment SELECT, journal SELECT/INSERT/UPDATE, selected
receipt SELECT/INSERT, business SELECT and declared-field UPDATE. The enrollment
check takes `SHARE UPDATE EXCLUSIVE`, which additionally requires table-level
UPDATE, DELETE or TRUNCATE. Correct both diagnostic profiles to check this
previously missing prerequisite; recommend UPDATE without unnecessary destructive
grants. Check origin replication mode, writable primary/default transaction
settings and absence of an active capture context. Readiness checks one leased
session; it cannot observe all sessions or past bypassed writes.

## Bounds and connection ownership

Reuse ten-second inspection/statement/lock budgets, 1 KiB table names, 64 KiB
reports and 64 affected objects/declared fields. Add a 256 UTF-8 byte limit for
environment/release context to `pkg/api/limits.go`, generated into SDK capture
configuration checks. Invalid options fail before SQL; unavailable dependencies
produce fixed redacted unknown findings. Blocked takes precedence over unknown;
ready requires every finding ready.

Go leases through database/sql with a context deadline. Node requires an idle
exclusive pool lease, bounds acquisition and all work, and discards late/busy
leases on timeout. Python requires idle/autocommit psycopg-compatible connections
and rejects active caller transactions before SQL. Python async adds an overall
inspection deadline; sync connection/I/O deadlines remain driver configuration.
Always roll back owned snapshots; failed cleanup marks the observation unavailable
and discards/closes a connection where the driver API permits. Never commit or
roll back a caller's transaction.

Run the check explicitly during importer setup and after configuration changes;
do not add it to each captured business transaction. Optional effect/milestone/
workflow outboxes are outside the base receipt check. Capture coverage, business
semantics, declared dependencies, executor readiness, fresh plan validation and
rehearsal remain independent requirements.

## Verification

Disposable PostgreSQL 16 tests exercise actual capture in Go, Node and Python
(sync and async) under restricted writer roles with only the four required
function grants. Those roles cannot create repair plans. Missing capture/receipt
grants, column-only updates, disabled triggers, schema drift, replica sessions
and read-only defaults produce explicit failures. Diagnostics preserve business
rows, versions, journals, receipts and saved plans. Modified installed helpers
and executor check SQL with advisory-lock side effects are never executed.
Node timeout tests cover late acquisition, busy-client discard and redaction;
Python checks caller-transaction preservation. The real CLI exercises capture
profile identity and ready/blocked exit codes. Existing executor authentication,
MFA, remote/profile isolation, rendering and repair acceptance remain covered.
