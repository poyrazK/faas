# ADR-2026101707: Read-only selective repair readiness

- Status: accepted for internal operator qualification
- Date: 2026-10-17
- Extends: [selective repair](20261016-selective-data-repair.md) and [history discovery](2026101706-repair-history-discovery.md)
- Scope: source diagnostics, standalone/hosted APIs, CLI and both review screens. No application-data, control-plane schema, approval or VM lifecycle change.

## Decision

Add `gregale data-repair doctor --table QUALIFIED_NAME`. Inspect one exact
owner-enrolled registry name using the executor's existing database connection.
Do not resolve caller-supplied SQL identifiers or accept SQL, roles, connection
strings, origins or check policies from diagnostic requests.

Return a versioned report with authenticated scope, table, observation time,
policy mode/count/hash and fixed findings: connection, PostgreSQL major version,
installation, function compatibility, enrollment, catalog fingerprint,
capture/version triggers, executor privileges and policy configuration. Hosted
reports add the exact current binding check. Every finding has `ready`,
`blocked` or `unknown`, a stable code, fixed summary, next action and affected
objects. Blocked takes precedence over unknown; ready requires every finding
to be ready. An unavailable or older executor remains unknown.

The implicit/default `SELECT true` policy is blocked until business checks are
declared. Static validation and hashing do not parse PostgreSQL semantics or
prove that explicitly configured queries are useful. An explicitly named no-op
policy remains operator configuration, not evidence.

## Catalog inspection

Use a read-only, repeatable-read transaction with fixed `pg_catalog` search path
and ten-second request, statement and lock budgets. Roll back on every exit.
Probe catalog prerequisites before querying the registry, so missing schemas,
columns, functions and grants can be diagnosed without installing a probe
function or requiring a successful repair call. The only source table read is
the exact enrollment metadata row; journal, plan and business rows are not read.
Never execute check SQL, `EXPLAIN`, installed repair functions, DDL or maintenance
actions. Read-only transactions alone cannot prevent nontransactional side
effects from arbitrary functions.

Derive required signatures, return types, language, body hashes, volatility,
security mode, search path and invocation properties from the binary's embedded
owner-install SQL. Reject missing, changed or overloaded definitions. Compute
the table fingerprint using that trusted embedded catalog query, without calling
an installed helper. Inspect capture/version trigger definitions, enablement and
the current replication mode. Check required repair-table column types and
regular-table shape, plus journal protection/finalization triggers. These are
compatibility observations, not a full database integrity audit or row/constraint
validation. The application-owner SQL exception from the original repair ADR
also covers these fixed catalog probes; control-plane queries still use sqlc.

Observe effective runtime privileges of the connected executor role: schema
usage; enrollment select; operations/changes/plans select, insert and update;
runtime function execute; business schema usage, table select and declared-field
update, plus table-level UPDATE, DELETE or TRUNCATE for the enrollment lock.
Column-only UPDATE does not authorize `SHARE UPDATE EXCLUSIVE`; the capture
readiness milestone corrects this prerequisite and recommends table-level UPDATE
without extra DELETE/TRUNCATE grants. Enrollment DDL and
maintenance privileges are not prerequisites. Separately configured SDK writer
roles and their replication settings are outside this report.

Bound table names to 1 KiB, queries to 16 KiB, reports to 64 KiB and object/field
lists to 64. Budgets live in `pkg/api/limits.go`. Dependency failures return fixed
redacted findings, never server errors, SQL bodies, credentials or row values.

## Authority and interfaces

Standalone authenticated `GET /v1/data-repair/doctor?table=...` uses its token's
fixed scope. Hosted `GET /v1/apps/{slug}/data-repair/doctor` requires PostgreSQL
read/admin authorization and MFA. Environment/customer selectors must match
one current operator-configured binding; ambiguous or unknown bindings are
refused. The binding finding comes from apid, not the remote report. An
unsupported, unavailable or mismatched remote produces structured unknown
observations for that binding. There is no retained-Operation prerequisite,
Operation creation, approval, reconciliation or audit decision. The route
remains outside the public SDK contract.

CLI prints JSON; exit zero requires ready. Blocked and unknown reports print
normally with exit one. No `--fix`, mutation, retry or idempotency key exists.
Both review screens request diagnostics explicitly, render plain text and discard
late results after identity/table/scope changes. A report cannot enable a repair
action, replace plan freshness, extend expiry or attach rehearsal evidence.
Remote identity, structure, size and redirect guards fail closed.

`ready` covers observed executor configuration only. Capture coverage, SDK
writer access, declared dependencies, business-check results, row/constraint
integrity and rehearsal remain separate requirements.

## Verification

Disposable PostgreSQL tests cover healthy/missing installations, explicit owner
upgrade, changed/missing functions, disabled capture and journal protection,
missing version guard, schema drift, RLS, replica session mode and removed
executor grants. Policies and installed helpers with advisory-lock side effects
are never executed. Saved plans, journals and business row versions stay
unchanged. Authentication, MFA, hosted binding and permission scope, remote
failure/redirect/size guards, CLI exit codes, plain-text rendering and stale
response isolation are exercised. The actual CLI journey checks readiness
before plan creation, disables/restores capture and verifies unchanged rows.
