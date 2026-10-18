# Selective data repair (internal)

Repair an explicitly recorded PostgreSQL update without replacing the entire
database. Gregale prepares candidate inverse updates, identifies later edits,
rehearses the candidates in an isolated copy, and applies an approved plan with
fresh row checks. A transport failure is resolved through a durable receipt.

The internal preview supports both an application-bound **operator executor**
and a hosted management workflow with durable Operations, account approvals,
review links and receipt reconciliation. Hosted admission is closed until the
operator installs exact account/app/environment/customer executor bindings.
The local Docker runner remains an operator qualification backend; production
rehearsal through schedd/vmmd still needs qualification. See
[ADR-20261016](adr/20261016-selective-data-repair.md).

## Supported contract

- PostgreSQL 16, one regular owner-scoped table, one source transaction.
- Explicitly declared update fields; no inserts/deletes inside recorded commands.
- A primary key, immutable inserted-row UUID, and version advanced on every update.
- Up to 1,000 distinct records and 4 MiB of captured key/before/after JSON per operation.
- Plans expire after 15 minutes. Whole rehearsal snapshots are bounded to 64 MiB.
- Candidate conflicts abort the entire approved correction. Plans never silently
  refresh versions or drop newly conflicting candidates.

RLS tables, partitioning/inheritance, custom triggers/rules/expressions, and
cascading updates of selected fields are rejected. Enrollment does not infer
cross-table dependencies or external actions in application code. The owner must
qualify the selected command's effects. Record before the incident: existing
operation receipts alone cannot reconstruct uncaptured old values.

Limits are defined in `pkg/api/limits.go`. Capture failure aborts the business
transaction. Readiness and rehearsal establish mechanical/database checks;
application semantics depend on the checks and dependencies the owner declares.

## Inspect repair readiness

Use **Executor readiness** on either review screen, or run the read-only doctor
command for one exact qualified enrollment name. The default profile inspects
the executor connection:

```sh
# Fixed-scope executor and its reader token:
gregale data-repair doctor --table public.products

# Hosted reader login and one current configured source binding:
gregale data-repair doctor --app products --environment production \
  --customer CUSTOMER_UUID --table public.products
```

The report returns `ready`, `blocked` or `unknown` findings, specific next actions
and affected objects. It checks PostgreSQL 16, required repair tables and function
definitions, field enrollment, catalog fingerprint, capture/version triggers,
executor-role runtime grants and static check-policy configuration. Hosted
results also confirm one exact current operator-configured binding. The
implicit/default `SELECT true` policy is blocked until business checks are
declared. Configured checks are hashed without executing or parsing their SQL;
configuration readiness cannot establish their correctness.

The CLI prints JSON and exits zero only for ready. Blocked and unknown reports
exit one. Missing installation points to explicit owner installation; missing
upgrades point to `gregale data-repair schema --upgrade`. Changed base capture
functions require owner review against the installation SQL; do not rerun initial
installation over an existing schema. Missing grants name the precise privilege;
disabled triggers and schema drift require owner investigation. Do not rewrite
stored fingerprints or reset row versions to clear a finding. Doctor performs
no repair or automatic upgrade.

Diagnostics use a read-only snapshot with a ten-second bound, fixed catalog
queries and the exact enrollment metadata row. They never read business, journal
or plan rows, execute installed repair/check functions, prepare a plan, rehearse,
approve, resolve an outcome or apply. Query strings are limited to 16 KiB, table
names to 1 KiB, reports to 64 KiB and affected-object/field lists to 64. Unknown
means inspection could not establish readiness. Unavailable or older hosted
executors return unknown rather than empty successful reports.

`ready` covers the **connected executor's observed configuration** at the reported
time. Check SDK writer permissions and session settings with the capture profile
below. Capture
coverage, row/constraint integrity, declared dependencies, business-check results
and rehearsal remain separate requirements. Readiness cannot grant approval or
enable an apply. See [ADR-2026101707](adr/2026101707-repair-readiness.md).

Authenticated APIs are `GET /v1/data-repair/doctor?table=public.products` and
`GET /v1/apps/{slug}/data-repair/doctor?table=public.products`, with optional hosted
`environment`/`customer` selectors. Hosted access requires `postgres:read` or admin
and the existing MFA restrictions. Scope, SQL, connection, role and automatic-fix
parameters are refused.

### Inspect an application writer

Run the capture profile against the application's actual writer connection,
using trusted scope and release configuration:

```sh
gregale data-repair doctor --profile capture \
  --database-env APP_DATABASE_URL --scope-file repair-scope.json \
  --table public.products --release import-v1
```

This is a local command. It does not accept hosted/API selectors or send the
writer connection to an executor. Add `--receipt-protocol customer` for Customer
Operations; the scope file must then include `platform_tenant_id`. Scope and
release identify the report's application context; they do not prove database
ownership or confer platform authorization. The report has `profile: "capture"`;
executor reports have `profile: "executor"`. Both exit zero only when ready.

Applications can inspect their own connection pool before enabling an importer:

```go
report, err := faas.CheckDataRepairCapture(ctx, writerDB, faas.DataRepairCaptureReadinessOptions{
    Scope: faas.DataRepairCaptureScope{
        AccountID: accountID, AppID: appID, Environment: "production",
    },
    Table: "public.products", Release: executingRelease,
})
// Handle err, then inspect report.Status and report.Findings.
```

```ts
import { checkDataRepairCapture } from '@gregale/sdk-node';

const report = await checkDataRepairCapture(writerPool, {
  accountId, appId, environment: 'production',
  table: 'public.products', release: executingRelease,
});
```

```python
from faas_sdk import check_data_repair_capture, acheck_data_repair_capture

options = dict(account_id=account_id, app_id=app_id, environment="production",
               table="public.products", release=executing_release)
report = check_data_repair_capture(writer_connection, **options)
async_report = await acheck_data_repair_capture(async_writer_connection, **options)
```

The Go import is `github.com/poyrazK/faas/sdk/go` (package `faas`). Go leases a
connection from `*sql.DB`; Node requires a pool that returns an idle, exclusive
connection. Python requires an exclusively leased idle/autocommit
psycopg-compatible connection and rejects an active caller transaction before
issuing SQL. Each helper owns and rolls back a read-only snapshot. Go and Node
release the leases they acquire; Python leaves a healthy connection idle for
its caller. Failed cleanup cannot produce a ready report. Go and Node bound
inspection to ten seconds; Python's async helper also has a ten-second
inspection deadline. Python's synchronous helper uses ten-second SQL statement
and lock budgets; configure connection/I/O deadlines in the driver.

The ten findings cover connection, PostgreSQL 16, capture installation and
function compatibility, the selected base receipt inbox, session settings,
effective writer grants, enrollment, fingerprint and triggers. Missing grants
list the exact privilege; the writer needs no plan, approval, apply or maintenance
access. It must have a table-level lock privilege as described in the grant
example below. Origin replication mode, a writable primary/default and no
leftover capture context are required. Environment and release are bounded to
256 UTF-8 bytes; table, report and object limits match executor diagnostics.

The probe reads catalogs and enrollment metadata, never business rows, journals,
plans or receipt payloads, and executes no installed helper or check SQL. It does
not install or upgrade anything. The SDKs embed the same generated fixed probe;
refresh it with `go run scripts/gen-data-repair-readiness.go` after changing the
capture compatibility contract. `--check` and `make test-data-repair` verify
generation freshness.

Run this explicitly after connection, role, session or schema changes; the
capture helper does not automatically run it on every transaction. A ready
report covers one observed session and the base managed/customer receipt inbox.
Receipt inspection checks catalog prerequisites; constraints and application
semantics still need qualification. Optional milestone/effect/workflow outboxes
need their own qualification.
Executor readiness, capture coverage, dependencies, business checks and rehearsal
remain separate. See [ADR-2026101708](adr/2026101708-repair-capture-readiness.md).

## Find a recorded operation

The executor review screen and hosted repair page include **Find a recorded
operation**. Search exact release/table names and capture creation times, inspect
the metadata, then select **Prepare repair** to enter the ordinary plan workflow.
The Operations dashboard links to this search.

```sh
# Fixed-scope local executor:
gregale data-repair sources --release buggy-import-v1 --table public.products \
  --since 2026-10-17T00:00:00Z --before 2026-10-18T00:00:00Z

# Hosted workflow, using the ordinary login:
gregale data-repair sources --app products --scopes
gregale data-repair sources --app products --environment production \
  --customer CUSTOMER_UUID --release buggy-import-v1
```

Hosted scope selection comes from the operator's exact account/app/environment/
customer bindings. With multiple matching bindings, choose `--environment` and
`--customer`; an explicitly empty customer selects an account-scoped binding.
The executor's token always fixes its own scope. Hosted discovery requires
`postgres:read` or admin authority, including the existing MFA restrictions.

Responses contain operation ID, release, qualified table, creation time, recorded
change count, payload availability, applied-repair count and latest receipt IDs.
`change_rows` is `null` after journal purge because the original count is no
longer available. Captured repairs also appear with their `repair_of` identity.
Hosted entries report `settled`, `unsettled`, `not_retained` or `repair_operation`;
only retained, settled source Operations with payload can offer preparation.
`can_prepare` is an admission hint. Recorded counts are not candidate counts;
planning still verifies enrollment and classifies current rows from scratch.

Pages default to 25 entries, with a maximum of 100 and a 512 KiB response bound.
Release/table filters and cursors are each bounded to 1 KiB.
Ordering is newest creation time first, then descending UUID for ties. `--since`
is inclusive and `--before` exclusive. Pass `next_cursor` as `--cursor`, keeping
the filters and page size unchanged. Continuations bind scope and filters;
hosted continuations also bind the configured executor generation/origin.
Changing those inputs rejects the continuation. Cursors grant no authority.
Listing is live metadata navigation; later commits may appear between pages.

For an existing installation, apply `schema --upgrade` as described below before
using discovery. The owner upgrade adds journal and receipt indexes; review their
build cost and locks against the intended database before rollout. Discovery reads metadata
without creating a plan or fetching business rows and recorded field values.

## Find every repair attempt

Use **Repair history** beside a capture, or enter its original operation ID in
**Find repair history** on either review screen. History includes approved,
closed, replaced, applied and compacted plans; it does not depend on whether
the original capture can still prepare a repair.

```sh
gregale data-repair history SOURCE_OPERATION_UUID
gregale data-repair history SOURCE_OPERATION_UUID --app products \
  --environment production --customer CUSTOMER_UUID --limit 25
```

Each entry contains its exact plan ID/digest, creation/expiry, frozen selection
counts, rehearsal and source approval times, approval actor, replacement links
and comparison, explicit apply closure, committed repair ID/time and payload
purge marker. Pages contain no keys, captured row values or business SQL.
Approval, closure, replacement and commit remain separate facts. **No committed
receipt or closure means the apply outcome is unconfirmed.** Expiry does not
prove rollback, and loading history never retries an apply or changes approval.

History uses the discovery page/byte limits above and orders newest plans first
with descending UUID ties. Pass `next_cursor` as `--cursor`, keeping the source,
scope and page size unchanged. Hosted continuations also pin executor binding
identity. This is live navigation: restart listing to find newer plans above a
saved boundary. A known source with no plans returns an empty page; an unknown
or out-of-scope source returns not found.

Hosted entries report execution `retained`, `not_retained` or `unavailable`
separately from source facts. A committed source receipt can coexist with an
unresolved hosted phase. Missing hosted history does not remove the source
receipt or prove an apply outcome. **Load plan**, **Load parent** and **Load
replacement** open retained source plans. **Open operation** is available when
the exact hosted execution and binding are retained.

History inspection in both screens is read-only. To take actions, explicitly
open the normal plan workflow with **Load plan** in the standalone controls or
**Open operation** in hosted history, preserving the original attempted repair
ID for uncertainty. Hosted historical plan views remain available when their ledger
has expired. They can inspect retained payload/metadata and verify a committed
receipt, but cannot recreate an Operation, resume a write or transfer approval.
Use the ordinary retained Operation workflow for further actions. CLI recovery
uses the same source-scoped read endpoints:

```sh
gregale data-repair get PLAN_UUID --app products --source SOURCE_OPERATION_UUID \
  --environment production --customer CUSTOMER_UUID
gregale data-repair verify PLAN_UUID --app products --source SOURCE_OPERATION_UUID \
  --environment production --customer CUSTOMER_UUID --digest PLAN_SHA256
```

These commands need PostgreSQL read/admin authority and the current exact
configured source binding. They work without a retained source or repair
Operation. `--source` and its scope selectors are accepted only for hosted
read-only `get`/`verify`. Missing repair journals remain explicitly unavailable
when verifying; source history and committed counts remain visible.

Existing installations need the explicit owner `schema --upgrade`. It installs
`list_history(uuid,jsonb,timestamptz,uuid,integer)` and a source/scope/creation
index without reenrollment or refreshing saved plans. Review index build cost
and locks before rollout. A qualified source reader needs EXECUTE on this
function and SELECT on `gregale_repair.operations` and `gregale_repair.plans`;
no business-table reads or maintenance/write grants are needed for history.
See [ADR-2026101706](adr/2026101706-repair-history-discovery.md).

## Review excluded records

New plans include a frozen reason, observed row version and observed values for
each record. The review screens let you view conflicts, missing records or
candidates separately. This filter changes the view; the approved correction
still includes every candidate in the saved plan.

| Reason | Meaning |
|---|---|
| `matches_recorded_write` | The original row identity, version and affected values match the recorded write. It remains a candidate subject to rehearsal, constraints and declared checks. |
| `record_version_changed` | The record version differs, including when another field changed or an affected value was edited and then changed back. |
| `recorded_values_changed` | Affected values differ even though the version matches. The record remains a conflict. |
| `record_missing` | The captured row identity is absent. Reusing its primary key for a new row does not make that replacement eligible. |

Observations contain only fields changed by the recorded operation. They are
bounded to 16 KiB per row and 256 KiB across the plan; an observation over either
budget is omitted with `observation_omitted: true`. Its reason, version and
classification remain available. Omission does not make a conflict eligible.
Exact JSON text preserves large integer keys and numeric values in the browser.

These values describe preparation time. Loading or replaying a saved plan never
refreshes them or its digest. A new plan gets new observations and requires its
own rehearsal and approval. The live apply still performs fresh candidate
version/value checks. Conflict review provides context for separate review;
it grants no overwrite authority over subsequent edits.

## Install and enroll

Use an isolated test application database first. Install as its owner:

```sh
export GREGALE_REPAIR_DATABASE_URL='postgresql://...'
gregale data-repair schema | psql "$GREGALE_REPAIR_DATABASE_URL" -X -v ON_ERROR_STOP=1
gregale data-repair enroll --table public.products --fields category
```

Installation and enrollment are explicit transactions and must each run once.
The schema command prints SQL and never connects by itself. Enrollment adds
`gregale_row_identity`, `gregale_row_version`, a unique identity index, and
capture/version triggers. Do not bypass triggers, reset versions, or reuse row
identities. Schema/trigger changes invalidate existing evidence; automatic
schema requalification is intentionally unavailable.

For an existing installation, review and apply the current owner upgrade:

```sh
gregale data-repair schema --upgrade > repair-upgrade.sql
psql "$GREGALE_REPAIR_DATABASE_URL" -X -v ON_ERROR_STOP=1 -f repair-upgrade.sql
```

The upgrade is transactional and repeatable. It preserves enrollment, row
identities/versions and existing plan digests, approvals and receipts. New plans
gain the current diagnostic fields. Older plans retain their original document;
the UI marks missing diagnostic observations as not recorded. No upgrade runs
from an application startup or request handler.

The schema grants nothing to PUBLIC. The initial executor uses a trusted owner
or owner-qualified repair role. To use a separate trusted backend writer:

```sql
GRANT USAGE ON SCHEMA public, gregale_repair TO application_writer;
GRANT SELECT, UPDATE ON public.products TO application_writer;
GRANT SELECT ON gregale_repair.enrollments TO application_writer;
GRANT SELECT, INSERT, UPDATE ON gregale_repair.operations, gregale_repair.changes TO application_writer;
GRANT EXECUTE ON FUNCTION gregale_repair.begin_operation(uuid,jsonb,text,regclass,uuid),
  gregale_repair.finish_operation(uuid), gregale_repair.check_enrollment(uuid),
  gregale_repair.fingerprint(regclass) TO application_writer;

-- The selected managed Operation receipt protocol also needs its base inbox:
GRANT SELECT, INSERT ON public.gregale_operation_inbox TO application_writer;
-- For Customer Operations, grant the same privileges on
-- public.gregale_customer_operation_inbox instead.
```

The owner must first install the selected SDK receipt schema. Optional outboxes
need separate grants. PostgreSQL trigger invocation does not require caller
EXECUTE grants on trigger functions. Capture's enrollment check takes a
`SHARE UPDATE EXCLUSIVE` lock: column-only UPDATE is insufficient. PostgreSQL
accepts table-level UPDATE, DELETE or TRUNCATE for that lock; the example uses
UPDATE and grants no unnecessary DELETE/TRUNCATE privileges. These are trusted
backend grants, not end-user database permissions. Do not grant evidence,
approval, plan-table writes or apply authority to untrusted clients. Database
owners remain part of the trust boundary. Captured values require an explicit
retention policy. The reviewed maintenance flow below removes eligible old
payloads while preserving identities and receipts; no automatic purge runs.

## Repair payload retention

Use `gregale data-repair schema --upgrade` for an explicit owner upgrade of an
existing installation; `retention schema` remains a compatible alias. Fresh
installations already include this contract. The
upgrade preserves enrollment and existing evidence; do not reenroll tables.

```sh
gregale data-repair retention plan --scope-file repair-scope.json --limit 32
gregale data-repair retention get RETENTION_PLAN_ID --scope-file repair-scope.json
gregale data-repair retention purge RETENTION_PLAN_ID --scope-file repair-scope.json \
  --digest REVIEWED_DIGEST --actor operator@example.com --yes
```

The local maintenance connection prepares a saved, data-free inventory before
purge. The minimum age is seven days; `--before RFC3339` can retain data longer.
Each inventory has at most 32 entries and 64 MiB of estimated logical payload,
expires after 15 minutes and is applied atomically with fresh state checks.
Changed entries reject the entire batch. No hosted API or executor token grants
purge authority.

Old applied plans and expired never-approved plans can lose their row items.
A superseded parent can also be compacted once its saved replacement is at
least seven days old (or older than `--before`). Retention verifies the source
fence, child integrity, same scope/source and digest-bound lineage/comparison.
The entry reports `superseded_plan`, `replacement_plan_id`,
`replacement_plan_digest` and `superseded_at`. Closing an apply without saving
a replacement keeps the parent's payload held. Approved unresolved plans stay
held after expiry unless a verified saved replacement fences further applies.

The inventory's bounded `holds` groups report `kind`, `reason` and `count`.
Reasons distinguish unresolved applies, closure without a replacement, recent
or unverifiable replacements, young receipts/unapproved plans, and journals
still required by an uncompacted plan. `held_approved_plans` counts approved,
unapplied plans currently held, excluding eligible superseded parents. Corrupt
selected documents or lineage reject inventory preparation and purge.

Source/inverse journals become eligible only after every plan referencing them
is compacted, including replacements. Review a new batch after plan compaction.
Business rows, enrollment and versions remain unchanged. Compaction preserves
original digests, approvals, checks, closure results, replacement links and apply
receipts. Same-ID closure, replacement and committed-apply recovery still work;
delayed applies against a superseded parent remain blocked. New planning from
a purged source returns `repair_payload_purged` (HTTP 410).

This removes logical row payloads. MVCC, WAL, backups and prior exports have
separate lifecycles. Identity/audit tombstones remain indefinitely in this
version. Native ownership journals and recovery reservations are outside the
purge. See the [retention runbook](runbooks/FaasDataRepairRetention.md) for grants,
unknown COMMIT handling and verification, and the
[retention decision](adr/2026101701-repair-retention.md) for the concurrency contract
and [superseded-plan compaction](adr/2026101704-repair-superseded-retention.md).

## Record an opted-in command

### Own capture and the receipt together

Use the repairable transaction entry point for new opted-in handlers. It starts
capture, calls the business handler, finalizes capture and binds it to the
operation receipt before COMMIT, using one connection and transaction:

```ts
import { withRepairableOperationTransaction } from '@gregale/sdk-node';

const response = await withRepairableOperationTransaction(pool, operationRequest, {
  table: 'public.products', environment: 'production', release: executingRelease,
}, async tx => {
  await tx.query('UPDATE products SET category=$1 WHERE id=ANY($2::bigint[])',
    [importedCategory, selectedProductIDs]);
  return { result: { imported: selectedProductIDs.length } };
});
```

```go
response, err := faas.WithRepairableOperationTransaction(ctx, writerDB, request,
    faas.DataRepairCapture{Table: "public.products", Environment: "production", Release: executingRelease},
    func(tx faas.OperationSQLTransaction) (faas.OperationOutcome, error) {
        _, err := tx.ExecContext(ctx, "UPDATE public.products SET category=$1 WHERE id=$2", category, productID)
        return faas.OperationOutcome{Result: json.RawMessage(`{"imported":1}`)}, err
    })
```

```python
from faas_sdk import with_repairable_operation_transaction

def import_products(cursor):
    cursor.execute("UPDATE public.products SET category=%s WHERE id=%s", (category, product_id))
    return {"result": {"imported": 1}}

response = with_repairable_operation_transaction(writer_connection, request,
    import_products, table="public.products", environment="production", release=executing_release)
```

Use only the supplied transaction/cursor for business writes, await database
operations and propagate errors. The callback must not control transaction
lifecycle or perform external actions.

Python also exports `awith_repairable_operation_transaction`. The result-only
Customer Operations equivalents are `WithRepairableCustomerOperationTransaction`
in Go, `withRepairableCustomerOperationTransaction` in Node, and
`with_repairable_customer_operation_transaction` /
`awith_repairable_customer_operation_transaction` in Python. Go and Python use
their existing receipt-bound Customer request context; Node uses its Customer
transaction request and preserves milestone/workflow validation callbacks.
The existing body/replayed result format and request digest protocols are unchanged.

The owner must explicitly install the current schema or apply
`gregale data-repair schema --upgrade`. In addition to the base writer grants:

```sql
GRANT SELECT, INSERT ON gregale_repair.capture_receipts TO application_writer;
GRANT EXECUTE ON FUNCTION gregale_repair.capture_transaction(uuid,jsonb,text,text,text,bytea,bytea,text)
  TO application_writer;
```

These entry points require an exact qualified enrollment name. Base capture
readiness continues to cover the lower-level capture/inbox contract; the new
entry points additionally check their receipt table/protection, privileges and
origin session mode before the business callback. No SDK function installs DDL
or grants access. Repair plan, approval and maintenance privileges are unnecessary.

Capture failures, callback errors, invalid results and receipt-binding failures
roll back business updates, journal rows and both receipts. Zero-row operations
still produce a finalized capture and binding. The immutable binding records
scope, release, table, enrollment, transaction identity and request/response
digests, with no row values or request/response bodies. PostgreSQL checks receipt
`xmin` only while binding the current open transaction; historical replay uses
durable metadata. Receipt protection is locked through commit, and capture's
enrollment/fingerprint is checked again after the callback.

A replay validates the saved receipt and capture binding before returning its
original bytes. It skips the handler and capture, including after payload
compaction or later business/schema edits. Supply the **original** environment,
table and capture release on retries; different capture configuration fails
explicitly. The caller already has the source capture ID as its operation ID.
Existing receipts without this binding return `repair_capture_missing`; they
cannot be retroactively attested. Their already committed work is preserved.

New capture failures use stable codes: `repair_capture_missing`,
`repair_capture_mismatch`, `repair_capture_incomplete` and
`repair_capture_unavailable`. Go exposes matching `ErrDataRepairCapture...`
sentinels; Node and Python expose `DataRepairCaptureError.code`. Guard/setup SQL
errors are redacted. A lost COMMIT response still uses the existing unknown
outcome error: retry the same operation identity and original capture
configuration to read its receipt, without repeating business writes.

Capture binding metadata stays with the indefinitely retained operation identity;
reviewed payload compaction does not remove it or make a replay execute again.
These guarantees cover handlers using these entry points and their declared
table/fields. Dependencies, business semantics and external actions still need
the owner's qualification. The existing repair review, rehearsal and approval
workflow applies. See [ADR-2026101709](adr/2026101709-repairable-operation-transactions.md).

### Compose capture inside an existing transaction

The SDK helper belongs **inside** the existing operation transaction callback.
Use only its supplied transaction and propagate errors:

```ts
import { withOperationTransaction, withDataRepairCapture } from '@gregale/sdk-node';

const response = await withOperationTransaction(pool, operationRequest, tx =>
  withDataRepairCapture(tx, operationRequest, {
    table: 'public.products', environment: 'production', release: executingRelease,
  }, async tx => {
    await tx.query('UPDATE products SET category=$1 WHERE id=ANY($2::bigint[])',
      [importedCategory, selectedProductIDs]);
    return { result: { imported: selectedProductIDs.length } };
  }));
```

Obtain `operationRequest` from the existing negotiated SDK factory only behind
Gregale's trusted ingress. Environment/release come from trusted execution
configuration. A replay returns the existing operation response without calling
capture again. Go exports `WithDataRepairCapture` and
`WithCustomerDataRepairCapture`; Python exports `with_data_repair_capture` and
`awith_data_repair_capture`. Node also accepts its negotiated Customer Operation
transaction request. No helper owns a transaction, performs DDL, or authenticates
arbitrary incoming HTTP requests.

## Start the internal executor

The operator machine needs a matching PostgreSQL 16 `pg_dump`, a PostgreSQL URI
in `GREGALE_REPAIR_DATABASE_URL`, and local Docker. The isolated restore preserves
database encoding and locale; an unavailable locale blocks rehearsal.
Pre-pull the immutable sandbox image; the runner never pulls during a request:

```sh
docker pull postgres@sha256:1a6ab3f5345eb6dbe04a1349529caabdb0ab09293a09590fad07b2246bfa4b54
export GREGALE_DATA_REPAIR_ENABLED=true
export GREGALE_REPAIR_TOKEN='a-random-operator-token-of-at-least-32-bytes'
gregale data-repair serve --scope-file repair-scope.json --actor operator@example.com
```

`repair-scope.json` fixes the authenticated scope for this executor:

```json
{"account_id":"11111111-1111-4111-8111-111111111111","app_id":"22222222-2222-4222-8222-222222222222","environment":"production"}
```

An optional `platform_tenant_id` must match the recorded source scope exactly.
Scopes and actors never come from a request body. Open
[the local review screen](http://127.0.0.1:8796), enter the executor token, and
prepare or load a plan. Review the exact record keys and recorded/restore values.
Conflicting/unavailable records are shown for review and are never selected for
automatic correction. Large PostgreSQL numbers render from exact JSON text.

To add a business assertion, supply `--checks-file repair-check.sql`. The file is
trusted operator SQL: one SELECT producing exactly one true boolean, for example:

```sql
SELECT NOT EXISTS (SELECT 1 FROM public.products WHERE category IS NULL)
```

Its exact bytes are hashed into the plan. The same check runs in rehearsal and
before the live commit. Changing it requires a new plan/rehearsal/approval.
Without a file, `SELECT true` adds no application-specific guarantee. Checks must
not perform external actions or acquire locks in an inconsistent order.
Assertions over other mutable records need explicit locking or database
constraints to remain valid through commit.

## Named business checks

Use `--checks-policy-file checks.json` to give each assertion a reviewable name.
It replaces `--checks-file` for that executor; the two options are mutually
exclusive. Existing single-SQL files and their plan hashes remain supported.
Apply the explicit owner upgrade before enabling a named policy.

```json
{
  "version": 1,
  "checks": [
    {
      "name": "Product categories restored",
      "sql": "SELECT count(*) FILTER (WHERE category = 'original') = 460 FROM public.products"
    },
    {
      "name": "Human edits preserved",
      "sql": "SELECT count(*) FILTER (WHERE category = 'human') = 40 FROM public.products"
    }
  ]
}
```

```sh
gregale data-repair serve --scope-file repair-scope.json \
  --actor operator@example.com --checks-policy-file checks.json
```

Policies contain 1–16 checks and fit within 16 KiB of JSON. Names are unique,
at most 64 ASCII bytes, start with a letter and use letters, numbers, spaces,
dots, underscores or hyphens. Names and query hashes remain with metadata and
receipts; keep secrets and row values out of names. Queries stay in trusted
operator configuration and authenticated rehearsal inputs.

Each query is one `SELECT` returning exactly one row, one boolean column, and
`true`. `false` or `NULL` fails the rule. Query errors, wrong result types and
wrong row/column counts produce `error`; remaining checks are `not_run` after
the first failure. Names, query bytes and declaration order bind the policy
hash; names and individual query hashes are also in the immutable plan digest.
A changed definition requires a new plan, rehearsal and approval.

Named evaluation uses a fixed `pg_catalog` search path. Qualify application
tables and functions explicitly, as in `public.products`. For a mutable policy
row, an owner-qualified assertion can use
`SELECT enabled FROM public.repair_policy WHERE id=1 FOR SHARE` to keep its
dependency locked through commit. Owners must review lock order and dependencies.
Checks are trusted, read-only assertions; this is not a sandbox for hostile SQL
or functions with external effects, and it does not infer application semantics.

Both review screens show each declaration, its SHA-256 and rehearsal/live result.
CLI `get` returns `checks`, `rehearsal.check_results`, `apply_checks` and any
`check_failure`. Reports contain only names, hashes and fixed statuses. Database
messages, SQL text and result values are excluded. Failed reports are review
context and cannot authorize approval. A failed rehearsal clears prior successful
rehearsal evidence for that unapproved plan.

The isolated runner evaluates the same policy after applying candidates. Live
apply repeats all checks before commit. A failed live check rolls back the
entire correction, its version advances and its new operation/receipt. The
executor attempts to retain its failure diagnostic after rollback; that write
is separate and may be unavailable during an outage. Successful live results
commit atomically with the receipt, and receipt replay never runs checks again.

Scheduled negative outcomes follow the existing cleanup and durable publication
flow. A lost publication response is reconciled from the retired task/report;
recovery never starts another guest. Publication of a failed report returns the
repair to review, with approval still blocked. Named policies require a runner
built with this check protocol; unsupported older readers fail closed. Legacy
single-SQL requests retain their original wire fields and evidence shape.

## CLI review and apply

### Verify a committed repair

After apply, observe the repaired records without changing the historical result:

```sh
gregale data-repair verify PLAN_UUID --digest PLAN_SHA256
gregale data-repair verify REPAIR_UUID --app products --digest PLAN_SHA256
```

Both review screens provide **Verify repaired records**. For example:

> 459 records restored at commit; 41 records excluded. At this observation,
> 458 repaired records are unchanged and one has a later edit.

`restored`, `excluded`, `applied_at`, `source_operation_id` and
`repair_operation_id` describe the committed receipt. `checked_at`, `available`,
`checked`, `unchanged`, `edited`, `missing`, `issues` and `rows` describe a new
observation. Only edited or missing repaired rows appear in `rows`. Versions
and exact key JSON stay strings; keys use the existing 16 KiB per-row and
256 KiB aggregate display limits. Current business values and check SQL are
excluded from the report.

Verification reads one read-only repeatable-read snapshot. It uses the repair
operation's own journal and compares immutable row identities, versions after
repair and recorded repaired values. An update to another field, or an edit
followed by restoration of the same value, still advances the version and counts
as `edited`. Missing rows count as `missing`; a newly inserted row reusing the
same primary key does not replace the old row identity. A same-version value
mismatch reports `recorded_values_changed` within `edited`. Original exclusions
are counted from the receipt and are not reread or promoted.

Plan compaction does not prevent verification while the repair journal remains.
After journal purge, `available` is false, `issues` contains `journal_purged`,
and observation counters and rows are empty. Schema/enrollment changes similarly
return `schema_changed` without inspecting business rows. Neither condition
changes the retained commit result or prevents same-ID receipt recovery. Plan
expiry and current check-policy changes do not invalidate a historical receipt;
verification does not rerun business checks or make a semantic correctness claim.

The CLI exits zero for an available observation, including later edits or missing
rows. It prints the unavailable report as JSON and exits one for journal/schema
unavailability. A missing committed receipt is a conflict: verification does not
infer rollback, close an apply, or reconcile an unknown hosted Operation. Use
the existing resolution/reconciliation workflow to settle that outcome.

The standalone endpoint is `POST /v1/data-repair/plans/{id}/verify`; the hosted
endpoint is `POST /v1/apps/{slug}/data-repair/operations/{id}/verify`. Both accept
only `{"digest":"PLAN_SHA256"}`. Hosted access requires PostgreSQL read or admin
permission, the normal MFA and same-origin checks, and the exact retained
executor binding. No approval, sequence or confirmation is required. An older
executor without this contract returns unavailable; clients do not fall back to
apply. Existing installations need the explicit owner `schema --upgrade`; it
adds `verify_plan(uuid,jsonb,text)` without reenrollment. A separate qualified
database reader needs execute permission on that function and the existing plan,
journal, enrollment, fingerprint and target-table read privileges. No startup
DDL or verification history is written. See the
[verification decision](adr/2026101705-repair-post-apply-verification.md).

### Check current drift

Run a read-only freshness check against the exact saved plan before attempting
the correction:

```sh
gregale data-repair validate PLAN_UUID --digest PLAN_SHA256
gregale data-repair validate REPAIR_UUID --app products --digest PLAN_SHA256
```

Both review screens provide **Check freshness** and show changed candidate
identifiers, expected/current versions and reasons. A reported blocking condition
disables approval and apply in that view. Loading a plan clears the tab's old
diagnostic; the server's live guards always remain authoritative.

Validation uses one read-only repeatable-read database snapshot. It compares
only the saved candidate set, reports schema/enrollment and configured check
definition changes, and identifies expired, applied, empty or purged plans.
Originally excluded records remain excluded. A changed plan digest is a conflict,
and another scope cannot read the report. Older executors without this endpoint
cannot supply a freshness report; the client does not fall back to rehearsal or
apply. Existing installations need the explicit owner `schema --upgrade` before
enabling this endpoint. The upgrade adds a function without reenrolling tables.

The JSON response contains `plan_id`, `plan_digest`, `scope`, `checked_at`,
`expires_at`, `matches`, candidate/exclusion counts, `checked`, `unchanged`,
`changed`, `issues`, and `rows`. Only changed original candidates appear in
`rows`. Reasons are `record_version_changed`, `recorded_values_changed`, or
`record_missing`; deleting and recreating a primary key does not preserve its row
identity. Exact key JSON and version strings preserve numeric precision. Key
display uses the existing 16 KiB per-record and 256 KiB aggregate diagnostic
budgets; omitted keys retain their row UUID and `key_omitted: true`. No current
business values or check SQL enter the report. Reports use the existing 1,000-row
and 16 MiB response bounds and are served with `Cache-Control: no-store`.

Replaced parents report `plan_replaced` and skip candidate reads. Their original
digest and approval remain available as history.

CLI validation prints JSON and exits 0 for a matching snapshot, or 1 for drift or
another blocking condition. Request/transport errors also exit 1 and report the
error on stderr. The HTTP endpoint returns 200 for a valid diagnostic, including
`matches: false`. Hosted validation requires `postgres:read` or admin authority,
with the usual MFA and same-origin checks; its request contains only `digest`
and does not require a mutation sequence or confirmation.

The report is advisory and remains separate from the saved plan, rehearsal,
approval, intent, and receipt. It executes neither inverse writes nor business
checks and does not extend expiry. `matches: true` means the checked mechanical
conditions matched at that snapshot, not that rehearsal or approval succeeded.
Changes can happen immediately afterward. Live apply still checks every original
candidate and every configured business rule before committing. A later edit
causes the entire correction to roll back. Prepare, rehearse and approve a new
plan to change the candidate selection; validation never silently drops a stale
candidate. Schema qualification and missing payload require operator review.

### Resolve an unsuccessful or uncertain apply

A failed apply response does not establish rollback. To stop retrying the old
plan and recover through replacement, explicitly resolve its original identity:

```sh
# Standalone: keep the same repair UUID used by the unsuccessful apply.
gregale data-repair resolve PLAN_UUID --digest PLAN_SHA256 \
  --repair-operation ORIGINAL_REPAIR_UUID --yes
# Hosted: the saved Operation supplies the original repair UUID.
gregale data-repair resolve REPAIR_UUID --app products --digest PLAN_SHA256 --yes
```

Both review screens provide **Resolve apply outcome**. The standalone screen
saves the original apply UUID in the page URL before submitting; preserve that
URL across reloads. CLI and browser actions make no automatic mutation retries.
`--yes` acknowledges permanent closure if the correction did not commit.

Resolution contacts the original configured executor and takes the same source
plan lock as apply. It returns either the existing matching committed receipt
or a durable `apply_resolution` with outcome `not_committed`, plan digest,
original repair UUID, resolution time and actor. The latter permanently closes
the plan before reporting success. Delayed requests, another apply UUID,
approval and rehearsal publication cannot reopen it. The original document,
digest, approval and expiry remain unchanged. Resolution runs no business checks
and makes no business-row changes. It cannot undo external effects.

A busy apply remains unresolved. An unavailable source, absent receipt, timeout
or lost closing response does not release authority. Repeat resolution with the
same identity and digest to recover its original result. A committed receipt
settles the hosted Operation as succeeded and cannot be replaced. Confirmed
closure settles it as `closed` with failure code `repair_not_committed`; prepare
a linked replacement, then rehearse and approve that new plan.

Hosted resolution requires management authority, normal MFA/same-origin checks,
the exact digest and initial `expected_sequence`. The CLI/review also need read
authority. The authenticated requester supplies the closure actor; an existing
receipt still has to match the original approval. Before the source call, the
Operation enters `resolving`, blocking apply and replacement. This
reservation and its events survive normal retention and appear in operator
health. Replays may use the original sequence. Read-only reconciliation can
recover an existing matching source result for a resolving reservation; it
never closes a source plan itself. Settlement starts the normal bounded
result/event retention window again, without refreshing source plan expiry.

Existing installations need the explicit owner `schema --upgrade`. No table
reenrollment is required. Closure alone keeps source payload held; a verified
saved replacement can later make its parent eligible for reviewed compaction
under the retention policy above. See the
[apply resolution decision](adr/2026101703-repair-apply-resolution.md).

### Prepare a linked replacement

After drift, prepare an explicit replacement of the saved plan:

```sh
# Generate and save a fresh UUID before submission. Reuse it after response loss.
gregale data-repair replace PLAN_UUID --digest PARENT_SHA256 \
  --repair-operation REPLACEMENT_UUID --yes
gregale data-repair replace REPAIR_UUID --app products --digest PARENT_SHA256 \
  --repair-operation REPLACEMENT_UUID --yes
```

Both review screens provide **Prepare replacement plan**, show the replacement
UUID before submission, and retain it in the page URL for recovery. Save that
UUID. Following an uncertain response, reload the parent and use **Resume
replacement** with the same UUID. CLI requests are not retried automatically.

The new plan includes a digest-bound `replacement` comparison with parent
identity/digest, `previous_candidates`, `retained_candidates`,
`removed_candidates`, and `previous_excluded`. Each item includes its
`previous_status`. For example, 460 candidates followed by one later human edit
become 459 candidates and 41 exclusions. Previously excluded records are copied
without rereading them and stay excluded across replacement chains. Their
observations remain historical; they are not refreshed to current values.

The new selection is still a candidate repair. It requires its own rehearsal and
approval, then fresh atomic apply guards and declared business checks. It binds
the currently configured policy and a new fixed expiry. A schema or enrollment
change requires operator qualification; replacement cannot bypass it. Purged
payload cannot be reconstructed. A matching freshness report alone grants no
rehearsal or approval authority.

The old document, digest, expiry and approval remain unchanged. A separate
`replacement_plan_id` lifecycle pointer disables further rehearsal publication,
approval and apply of the parent. The child and parent fence commit atomically
under the same plan lock as apply. A committed correction cannot be replaced.
An in-flight application must settle before the source can prepare a child.
Repeated replacement requests with the same identity return the original child,
including its fixed expiry or eventual receipt; another child UUID conflicts.

Hosted replacement requires `postgres:manage` or admin, normal MFA/same-origin
checks, the initial `expected_sequence`, and the child UUID as `Idempotency-Key`.
The CLI and review screens also read the parent plan: use admin authority or a
credential with both `postgres:read` and `postgres:manage` for that workflow.
It first reserves that UUID in the parent Operation's `replacing` phase. Active
rehearsal and applying/unknown/resolving outcomes block replacement. A missing receipt or
elapsed timeout does not release an uncertain apply. Use its existing receipt
reconciliation workflow for a matching commit, or explicitly resolve and close
the uncommitted plan first. A pending replacement stays reserved on failure
and accepts only the same UUID/digest; replays may use the original sequence.
It appears in operator health with recovery guidance and survives normal ledger
cleanup. Child admission and the parent's `replaced` phase commit together. An
expired child recovered after response loss is recorded as failed; its expiry
and approval authority are not refreshed.

Existing installations require the explicit owner `schema --upgrade`, which adds
a nullable unique plan reference and replaces the guarded SQL functions. No table
reenrollment is needed. The new metadata stays out of existing strict execution
headers; source snapshots retain the complete saved document.

Replacement never purges captured values or changes a saved retention selection.
After the retention minimum measured from the saved replacement, the separate
reviewed maintenance workflow can compact its parent. Closure without a saved
replacement and unresolved plans remain held. The replacement still pins its
source journal until its own payload becomes eligible and is compacted.

The CLI and browser use the same authenticated HTTP endpoints. HTTPS is required
for a remote executor. The local default is loopback HTTP. Tokens are read from
an environment variable, never passed in URL query strings or followed through
redirects. Commands make no automatic mutation retries.

```sh
gregale data-repair plan ORIGINAL_OPERATION_UUID
gregale data-repair rehearse PLAN_UUID --digest PLAN_SHA256
gregale data-repair approve PLAN_UUID --digest PLAN_SHA256 --yes
# Generate and save a new UUID before sending this request.
gregale data-repair apply PLAN_UUID --digest PLAN_SHA256 --repair-operation SAVED_REPAIR_UUID --yes
gregale data-repair get PLAN_UUID
```

Approval records the authenticated operator, exact digest and timestamp. Apply
uses the original candidate list and checks every identity/version again. A
later edit aborts all selected inverse writes, with no repair receipt. Prepare a
new plan and review its changed candidate/conflict selection.

After a network failure, inspect `get PLAN_UUID`. `applied_at` and
`repair_operation_id` establish a committed correction. Repeat apply only with
the same plan digest and saved repair UUID; an already committed request replays
its receipt without changing data or rerunning checks. A different repair UUID
cannot reapply that plan. A received error alone does not establish rollback
when commit status is unknown.

## Application HTTP integration

`pkg/apid/datarepair.Handler` can be mounted by an explicitly configured executor.
Its required authenticator supplies scope, actor and approval/apply permissions.
`Bearer` is the included single-scope operator adapter; applications can provide
their own validated identity adapter. This executor API is separate from the
hosted account API described below.

| Method and path | Request |
|---|---|
| `GET /v1/data-repair/sources` | Optional `release`, `table`, `since`, `before`, `limit`, `cursor` query parameters |
| `GET /v1/data-repair/sources/UUID/history` | Optional `limit`, `cursor`; metadata only |
| `POST /v1/data-repair/plans` | `{"operation_id":"UUID"}` |
| `GET /v1/data-repair/plans/UUID` | None |
| `POST /v1/data-repair/plans/UUID/validate` | `{"digest":"SHA256"}`; read-only drift report |
| `POST /v1/data-repair/plans/UUID/replace` | `{"digest":"PARENT_SHA256","plan_id":"SAVED_REPLACEMENT_UUID"}`; requires apply authority |
| `POST /v1/data-repair/plans/UUID/rehearse` | `{"digest":"SHA256"}` |
| `POST /v1/data-repair/plans/UUID/approve` | `{"digest":"SHA256"}` |
| `POST /v1/data-repair/plans/UUID/apply` | `{"digest":"SHA256","repair_operation_id":"UUID"}` |

Success returns the plan and receipt metadata. Errors use stable RFC 7807 codes:
`repair_invalid`, `repair_not_found`, `repair_conflict`, `repair_unavailable`,
`repair_unauthorized`, `repair_check_failed`, or `repair_outcome_unknown`.
Approval/apply permission failures use `repair_approval_forbidden` and
`repair_apply_forbidden`. Caller-supplied evidence,
scope, actor, unknown fields and trailing request documents are rejected in the
standalone mode. Only an executor explicitly started with
`--managed-coordinator` accepts forwarded approval and resolution actors from
its authenticated coordinator. That credential grants repair authority and must remain in operator
configuration, outside browsers and customer applications. Before
and after values remain sensitive: authorized responses use `Cache-Control:
no-store`; review tokens and snapshots are never saved in browser storage.

## Hosted management workflow (internal preview)

The hosted workflow starts from a **settled, retained Customer Operation** whose
business transaction used the capture adapter. Its account, app, environment
and customer must match the executor's capture scope exactly. A running or
unsettled source is refused. Standalone captures without a managed source keep
using the executor workflow above.

An operator configures an application-bound executor with
`--managed-coordinator` and a private credential used only by apid and the scheduled rehearsal worker. This mode
lets apid forward an approval actor already recorded by the Operations approval
store, and the authenticated requester for source resolution. Use a qualified isolated runner for the intended deployment; the local
Docker executor is for qualification. Apid does not run Docker or call vmmd.

Set `data_repair_bindings_path` in apid's TOML configuration, or
`FAAS_DATA_REPAIR_BINDINGS_PATH`, to an operator-owned JSON file:

```json
{
  "version": 1,
  "bindings": [{
    "scope": {
      "account_id": "11111111-1111-1111-1111-111111111111",
      "app_id": "22222222-2222-2222-2222-222222222222",
      "environment": "default",
      "platform_tenant_id": "44444444-4444-4444-4444-444444444444"
    },
    "generation": "55555555-5555-5555-5555-555555555555",
    "url": "https://repair-executor.internal.example",
    "token_env": "GREGALE_MANAGED_REPAIR_EXECUTOR_TOKEN"
  }]
}
```

Empty configuration leaves admission closed. Duplicate/unknown fields, duplicate
scopes, missing credentials and invalid destinations fail startup. The executor
token needs at least 32 bytes. HTTPS is required except on loopback for local
qualification. Redirects never receive credentials. Bindings are exact, with
no wildcard account/environment/customer selection. The generation and origin
are pinned in each repair; changing either makes previous repairs unavailable
through that binding. Restore the original binding to reconcile its receipts.

Hosted control-plane installation uses the ordinary migration flow, including
the native execution-family migration. Qualify its locks and scan cost against
the production Operation ledger before rollout. Application enrollment is still
explicit and is never installed by the management API.

Hosted CLI reviewers need both `postgres:read` and `postgres:manage`, or admin
authority. Readers need `postgres:read`; preparation, rehearsal and apply need
`postgres:manage`. Account sessions and admin keys follow the usual management
API rules, including pending-session MFA and same-origin browser mutation
checks. Approvals use the existing Operation approval endpoint with
`postgres:manage` or admin authority. A deployment-only key cannot approve a
repair. No customer self-service repair endpoint is exposed.

| Method and path | Request |
|---|---|
| `GET /v1/apps/SLUG/data-repair/source-scopes` | None; returns configured source scopes for this account/app |
| `GET /v1/apps/SLUG/data-repair/sources` | Optional `environment`, `customer` plus the executor discovery filters |
| `GET /v1/apps/SLUG/data-repair/sources/UUID/history` | Optional configured `environment`, `customer`, plus `limit`, `cursor`; source metadata and separate hosted execution context |
| `GET /v1/apps/SLUG/data-repair/sources/UUID/plans/PLAN_UUID` | Optional configured `environment`, `customer`; read-only `plan` and `execution` availability |
| `POST /v1/apps/SLUG/data-repair/sources/UUID/plans/PLAN_UUID/verify` | Optional configured `environment`, `customer`; body only `{"digest":"SHA256"}`; read-only committed repair observation |
| `POST /v1/apps/SLUG/data-repair/operations` | `{"source_operation_id":"UUID"}` and a saved UUID `Idempotency-Key` |
| `GET /v1/apps/SLUG/data-repair/operations/UUID` | None |
| `POST /v1/apps/SLUG/data-repair/operations/UUID/validate` | `{"digest":"SHA256"}`; read-only drift report |
| `POST /v1/apps/SLUG/data-repair/operations/UUID/replace` | `{"digest":"PARENT_SHA256","expected_sequence":1}`; `Idempotency-Key: SAVED_REPLACEMENT_UUID` |
| `POST /v1/apps/SLUG/data-repair/operations/UUID/rehearse` | `{"digest":"SHA256","expected_sequence":1}` |
| `POST /v1/apps/SLUG/operations/UUID/approvals/repair` | `{"expected_generation":1,"decision":"approved"}` or `"rejected"` |
| `POST /v1/apps/SLUG/data-repair/operations/UUID/apply` | `{"digest":"SHA256","expected_sequence":4}` |
| `POST /v1/apps/SLUG/data-repair/operations/UUID/reconcile` | Current digest and `expected_sequence` |
| `POST /v1/apps/SLUG/data-repair/operations/UUID/resolve` | Current digest and initial `expected_sequence`; permanently closes an uncommitted plan |

Successful reads/actions return `operation`, the exact `plan`, and status,
timeline, approval and review URLs. Sequence numbers above illustrate the
ordinary journey; always use the current returned sequence. The saved UUID is
the platform repair Operation ID, plan ID and application repair receipt ID.
Reusing it never refreshes candidates or expiry. Normal Operation reads and
timelines remain available even if the executor is offline. The Operations
dashboard links source operations to preparation and repairs to their plan.
The review page uses the existing account session and keeps values in memory.

Use the usual Gregale login for hosted CLI commands:

```sh
# Generate and save this UUID before submitting the first request.
gregale data-repair plan SOURCE_OPERATION_UUID --app products --repair-operation SAVED_REPAIR_UUID
gregale data-repair get SAVED_REPAIR_UUID --app products
gregale data-repair rehearse SAVED_REPAIR_UUID --app products --digest PLAN_SHA256
gregale data-repair approve SAVED_REPAIR_UUID --app products --digest PLAN_SHA256 --yes
gregale data-repair apply SAVED_REPAIR_UUID --app products --digest PLAN_SHA256 --yes
gregale data-repair reconcile SAVED_REPAIR_UUID --app products --digest PLAN_SHA256
gregale customer-operations timeline SAVED_REPAIR_UUID --app products
```

After a failed apply response, refresh and reconcile, or explicitly resolve the
outcome to close an uncommitted plan before replacement. A matching committed
application receipt settles the platform Operation as succeeded. An absent
receipt leaves it unresolved; reconciliation never retries writes. An explicit
same-ID apply can replay the receipt or retry that same approved transaction.
After an abandoned `applying` claim, reconcile waits for the execution bound
before making same-ID retry available. Generic Operation recovery/cancellation
refuses repair executions, so operators cannot declare success or dispatch a
customer handler to resolve an uncertain database commit. Expired plans that
have not started apply settle as failed when reconciled; they need a new review.
Scheduled rehearsals finish cleanup before their Operation can leave rehearsal.

## Scheduled rehearsal through schedd and vmmd

This internal runner is implemented but **not qualified for production activation**.
Use it only on designated qualification hosts until native execution and crash
recovery evidence are complete. The local Docker runner remains available for
operator development.

Set `"scheduled_rehearsal": true` on the exact binding above. Restart apid with
that binding, and provide schedd the same `FAAS_DATA_REPAIR_BINDINGS_PATH` and
executor token. Switching the mode changes the binding identity; existing repairs
keep their original binding. Start a new repair when changing modes.

Publish a platform PG16 base from
[`images/data-repair-postgres16.Dockerfile`](../images/data-repair-postgres16.Dockerfile)
using the existing isolated image/build pipeline, with the current static
`guest/init` installed as `/init`. Provide a separate, clean writable overlay
layer no larger than 1 GiB. Keep the configured artifacts immutable and pre-stage
them on the compute node; this runner refuses missing or changed local bytes.
Set these release-owned values on **both schedd and vmmd**:

```text
FAAS_DATA_REPAIR_RUNNER_BASE_KEY=<immutable base storage key>
FAAS_DATA_REPAIR_RUNNER_LAYER_KEY=<immutable overlay layer storage key>
FAAS_DATA_REPAIR_RUNNER_KERNEL_SHA256=<64 hex digits of vmmd's configured kernel bytes>
FAAS_DATA_REPAIR_RUNNER_BASE_SHA256=<64 hex digits of base ext4 bytes>
FAAS_DATA_REPAIR_RUNNER_LAYER_SHA256=<64 hex digits of layer ext4 bytes>
```

Vmmd also requires its existing `native_process_recovery` mode and a registered
compute identity. Partial runner metadata fails startup. Finally set
`FAAS_DATA_REPAIR_REHEARSAL_DISPATCH=1` on schedd. Its absence disables new VM
creation; cleanup of recorded attempts remains enabled. Missing or mismatched
runner capability leaves queued work pending until it is cancelled or expires.

The usual `rehearse` action now returns promptly with a durable task ID and a
`queued` status. Refresh the repair to observe `running`, `cleanup`, `publishing`
and completion. Successful publication moves the Operation to
`awaiting_approval`; it still requires the usual account approval before apply.
The review screen and CLI expose cancellation:

```sh
gregale data-repair rehearse SAVED_REPAIR_UUID --app products --digest PLAN_SHA256
gregale data-repair get SAVED_REPAIR_UUID --app products
gregale data-repair cancel-rehearsal SAVED_REPAIR_UUID --app products --digest PLAN_SHA256
```

Cancellation is durable and takes effect through schedd. Capacity remains held
until physical cleanup is confirmed. Cancellation is saved before refreshing
the source plan; an unavailable refresh can return an error while the intent
remains recorded in the Operation. There is one active rehearsal per account
and one physical rehearsal per node. Each reserves 512 MiB plus VM overhead,
one vCPU, 1000 millicores and a 1 GiB guest workspace ceiling, with a 120-second
execution bound. An expired lease never authorizes a replacement VM. A failed
attempt can be retried explicitly with a new task ID, preserving the plan's
original selection and expiry.

The executor exports a consistent dump with no source credentials. Schedd streams
it in memory to a fresh networkless PG16 VM. The guest restores that dump, applies
the exact inverse plan and executes the configured strict boolean check. Source
row values/check SQL never enter task records, host journals or logs. Host
retirement evidence binds the task, snapshot, plan, checks, node and runner image
bytes. Source writes remain guarded by fresh row versions at live apply time.

After a lost VM response, the worker only retires the original attempt; it does
not rerun it. After a lost evidence-publication response, it reads the source
plan and finishes the saved publication. Recovery preserves original placement
when bindings change and runs with dispatch disabled. Generic Operation
reconciliation cannot release an active scheduled task. Active task records also
block owner deletion until cleanup, while terminal history follows normal
Operation retention.

New Linux repair cold boots record a version-two producer identity before any
helper can run: the daemon PID, UID and kernel start time, bound to the original
physical generation and task journal. After a vmmd crash, recovery verifies that
daemon's exit using a Linux pidfd, retires the original helper cgroups and resource
references, then acknowledges cleanup. It never signals a reused daemon PID or
boots another attempt. A live original daemon, failed probe, changed binding,
missing journal or unconfirmed helper retirement keeps the task in `cleanup`
with its capacity charge.

Older unqualified native generations retain their existing cleanup refusal.
This enrollment covers the fixed repair cold-boot path; native builder export
remains disabled. Application repair payload retention is implemented above;
native ownership journals remain protected. Native KVM/crash qualification
remains a prerequisite for a deployed customer preview.

## Operator health

The operator-only `/dashboard/admin` page shows active repair phases, phase ages,
original task/node IDs, failure codes, cancellation intent, retained capacity,
and recovery guidance. Its JSON equivalent is `GET /v1/admin/ops/data-repairs`,
behind the admin scope, operator allowlist and existing MFA checks. `limit` and
`cursor` page detail rows; summary counts cover every active repair.

Schedd observes this durable state independently of the rehearsal worker, even
with dispatch disabled. Phase clocks survive retries; old records conservatively
fall back to creation time. Prometheus rules flag prolonged work, unavailable
original nodes, unknown apply outcomes and stale health reads. No source data or
native lease/producer frames enter the view or metric labels. Read errors remain
unavailable instead of becoming healthy zeroes. See the
[repair health runbook](runbooks/FaasDataRepairStuck.md) for alert timing and
recovery steps. Monitoring never authorizes replay, reservation release or
journal deletion.

## Qualification

Run the portable PostgreSQL contract/API tests with a disposable `DATABASE_URL`:

```sh
go test ./pkg/datarepair ./pkg/apid/datarepair
GREGALE_DATA_REPAIR_DOCKER_TEST=1 go test -count=1 -run TestSelectiveRepairRealIsolatedRehearsal ./pkg/datarepair
```

The explicit Docker acceptance uses a private test database, repairs 460 products,
preserves 40 subsequent human edits, and verifies rehearsal left the source
unchanged. See the [runnable import example](../examples/data-repair-import/README.md).

`make test-data-repair` runs the strict gate: PostgreSQL/race tests, all three SDK
capture adapters, required-capture transactions and writer readiness under restricted roles, an actual
CLI-to-executor rehearsal/approval/apply journey,
and all hosted API journeys with real control-plane and application databases,
real isolated rehearsal, durable task recovery and a hosted CLI read. The scheduled
journey runs the actual guest process in an isolated local container and injects
lost VM results, failed cleanup and lost evidence publication.
It requires installed Node SDK dev dependencies and a Python interpreter with
the SDK dependencies plus `psycopg`. Override `PYTHON` or
`GREGALE_DATA_REPAIR_PG_DUMP` when those executables are not on PATH.


On a designated native Linux x86_64 KVM host, select the same pinned runner's
absolute paths with `FAAS_TEST_KERNEL`, `FAAS_TEST_BASE_ROOTFS` and
`FAAS_TEST_LAYER_ROOTFS`, set `FAAS_TEST_FC_VERSION`, the five runner metadata
variables above, and a disposable PG16 `DATABASE_URL`. Use a Git checkout that
contains the changes to be tested and quiesce the dedicated host's Gregale
daemons through its existing maintenance procedure. The runner refuses active
or unobservable daemons and does not change service state. Then run:

```sh
GREGALE_REPAIR_NATIVE_EVIDENCE_DIR=/var/tmp/gregale-repair-native-RUN_ID \
  make test-data-repair-native GO=/path/to/pinned/go
```

The evidence directory must be empty and outside the source checkout. Omit the
variable to allocate a private temporary directory whose path is printed.
`python3 scripts/ci/run-data-repair-native.py --preflight-only` performs the
read-only prerequisites check and records a separate report. Preflight success
does not qualify execution.

The runner holds the shared acceptance-host lock. Before testing it checks root,
native KVM, the host marker, cgroups v2, required tools, the patched Go compiler
from `go.mod`, Firecracker/jailer versions, PG16 `pg_dump`, runner path/digest
bindings and baseline leak status. It keeps `native.log`, both leak-check logs,
`source.json`, `preflight.json` and `result.json`, including on failure. The
source fingerprint includes tracked and nonignored uncommitted files; HEAD
alone does not identify a worktree with changes.

A pass requires ordered Go JSON execution/pass events for all four selected
tests, successful execution of all four nested crash phases, zero native command
failures, before/after leak checks and unchanged source/image/binary hashes.
Missing, filtered or skipped tests fail. The final leak check runs after a test
failure or interruption too. Logs redact the fixture connection and password;
reports contain no database connection string or environment dump. Evidence
records its scope as Manager/backends; serving-daemon and physical-node failure
qualification remain separate. Existing evidence and uncertain resource journals
are never erased by the runner.

The native fixtures need root, `unshare`, a private mount namespace, and writable
scratch space beside the pinned layer on the same ext4/XFS/Btrfs filesystem.
They create a separate short-path tmpfs jail and a private persistent image-claim
directory. They preserve both directories if retirement cannot establish cleanup;
the failure output names their paths. Do not remove those journals to clear a
failed acceptance run.

This refuses an unqualified host before execution. It runs the real VM guest
against a private 500/40 fixture, verifies live apply, cancellation after boot,
replay fences, unchanged image bytes and zero leaked resources. A second test
kills an owner process after a real helper cgroup has crossed its execution
gate, then recovers and retires that original helper before releasing the slot.

`TestMetalDataRepairCrashRecovery` adds four deterministic SIGKILL checkpoints:

| Checkpoint | State before the lifecycle owner is killed |
|---|---|
| `boot` | Real Firecracker has started; Manager has not published the live instance. |
| `rehearsal` | The full request has crossed real vsock; the host has not read the guest result. |
| `cleanup` | VM exit is durably confirmed; resource retirement is unfinished. |
| `cleanup_ack` | Resource removal is durably confirmed; task retirement and the response are unfinished. |

The response-witness checks can also run without KVM:

```sh
go test -tags=metal -race -run '^TestDataRepairCrashResultWitness$' ./pkg/fcvm
```

These processes host vmmd's production Manager and native backends with test-only
pause wrappers; they do not start the serving vmmd binary or exercise gRPC/systemd
restart behavior. Owner and replacement share the original private mount
namespace. Each case verifies original PID/generation and producer ownership,
SIGKILL termination, restart inventory, refusal to replay under either daemon
incarnation, capacity held until resource proof, and idempotent retirement.
The boot case also proves Firecracker survives the owner's death. Source rows,
row versions and the 40 human edits remain unchanged, no rehearsal/approval/apply
authority is published, pinned images retain their hashes, and leak checks run
after retirement. A lost cleanup acknowledgement recovers its existing physical
receipt without reserving capacity again or starting another VM.

Portable tests also exercise actual owner process death, PID reuse, missing or
changed ownership, publication failure and uncertain helper cleanup. Compiling
these native tests or passing portable/container tests does not establish native
qualification. Record an actual designated-host gate result before rollout;
serving-daemon and physical-node failure exercises remain separate requirements.

A retirement with no original host journal is accepted only in the same daemon
incarnation that could have received the task. Missing history after a daemon
change or physical-node replacement is unconfirmed and retains the task.
