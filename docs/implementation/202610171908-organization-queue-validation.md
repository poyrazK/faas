# Organization queue validation

Local implementation of [ADR-202610171908](../adr/202610171908-organization-queue-ownership.md),
2026-10-17, using Go 1.26.9 and PostgreSQL 16.

The implementation retains queue, private consumer and environment runtime
identities, messages, receipt hashes and dead-letter history. Reservations
capture the organization's financial subject; adoption moves only the company
contributions of existing counters. The twenty reviewed organization operations
are available in OpenAPI and the Go, Node and Python SDKs.

Completed checks:

- The migration applies to a disposable PostgreSQL database and canonical
  schema generation succeeds.
- Memory/PostgreSQL state regressions pass with race detection: stable bindings,
  accepted messages, held receipts, stage runtime identity, retry and replay
  history through creator erasure; personal queue erasure; revoked request
  fences; quota adoption, shared cap boundaries and concurrent trigger admission
  across creator/organization namespaces. Unreviewed external triggers continue
  to block erasure. Related queue, receipt, offboarding and ownership regressions
  also pass.
- Backfill tests retain roots and counter contributions, reject mixed ownership
  and inconsistent counters, enforce immutable binding ownership and reject
  rollback while company reservations remain outstanding.
- Memory/PostgreSQL HTTP acceptance passes with race detection: an existing
  worker token and teammate acknowledge pre-erasure receipts and consume the
  retained backlog. A human enqueue paused after authorization returns 403 after
  removal and creates no message. Role/scope boundaries, organization aliases,
  aggregate usage and stage configuration remain enforced. Legacy trigger APIs
  cannot expose company projections through personal creator credentials.
- API clients, authorization, application access, lease service and account
  exports pass their component suites with race detection. All twenty-five
  queue action/role combinations and the complete action inventory are checked.
- Existing personal-account cap and reservation-release conformance cases pass
  in both stores. OpenAPI lint reports zero errors, with existing repository-wide
  warnings; route/schema parity and typed SDK coverage pass.
- Deterministic SQL generation and focused migration schema isolation and issued
  source adaptation checks pass. The disposable schema database was removed;
  the shared fixture database's public schema still has zero application tables.
- The complete Go SDK suite passes with race detection.
- Node: build plus 419 client tests and eight generator postprocessor tests.
- Python: organization queue, object storage and PostgreSQL client tests pass.
  Generation ran in an isolated temporary directory; only newly generated
  modules and the generated model barrel were copied back, preserving existing
  handwritten files and tests.
- Runtime documentation, ADR numbering, sealed-environment scope, text encoding,
  shell quoting, runbook SQL, durable-entity SDK contracts and whitespace checks
  pass. The previous workspace changes remain present.
- All configured Go static checks ran across the eleven changed packages.
  Focused context and export reruns pass after forwarding worker completion's
  authorization context and documenting intentional exclusion of malformed
  historical queue references.
- Focused race regressions pass after the context fix: worker serving receipts,
  pull completion and retained organization ownership. The pull completion test
  explicitly rejects a revoked authorization context before accepting the valid
  attempt; account export tests also pass.

Production deployment and native KVM acceptance are pending; the HTTP fixture
creates a live worker record without booting a VM.
Async admission and dispatch must be paused during the schema change, with all
quota writers restarted together before resuming. Mixed old/new quota writers
are unsupported; rollback must precede creator erasure.

## Release branch verification

[PR #4882](https://github.com/poyrazK/faas/pull/4882) combines the eight enterprise
slices with main at `28cd8cb3c`. Their ADR identifiers are now
`202610171901` through `202610171908`, avoiding concurrent decision numbers.
The seven ownership migrations follow main's migration tail and recognize
completed schema expansions during replay without overwriting later functions.

Fresh PostgreSQL 16 schema generation, deterministic SQL bindings, apply-and-walk
and replay of all seven migrations pass. The three historical migration fixtures
previously rejected by membership/project guards now create valid personal
organization parents and pass. A new race regression rolls all seven ownership
migrations down and up, preserving main's incoming migration capacity accounting,
restoring organization policy and retaining identical constraint definitions.
The complete historical migration package has not been rerun.

All seven affected daemon entry points build with the metal tag. Node build and
the three organization client tests pass, as do the three Python organization
client tests, ADR uniqueness, core test citations, Go formatting, embedded
OpenAPI parity and whitespace checks. Production rollout and native runtime
acceptance remain pending.

Organization state and HTTP regressions also pass with race detection on
`a23dea529`, including current membership, shared quotas, retained worker
receipts and creator erasure. OpenAPI route/schema compliance and typed SDK
coverage pass on that source, as do repository policy and runtime documentation
contracts. Main's subsequent storage and release-compatibility changes are
integrated from `5f10b1f27`. The ownership migrations now bound lock waits to five
seconds and document their atomic-maintenance exceptions; production sizing
and pause-duration qualification remain deployment prerequisites.

The final integration also includes main's event SDK changes at `80ce06bad`;
both SDK export sets are retained and the Node build passes. Migration apply,
replay and the complete ownership down/up recovery regression pass after the
compatibility updates. The protocol check matches all ten services, 296 messages,
five enums and 156 guest JSON keys against this base. No ancestor `v*` release
tag was available, so that comparison does not establish compatibility with an
independently identified deployed release.

## PR #4882 CI follow-up

The first CI run exposed row locks against the nullable creator join in
`app_resource_accounts`. Admission and logging observation queries now lock the
actual organization or legacy personal account, then recheck the app namespace.
PostgreSQL regressions cover personal billing, adopted company billing, erased
creators, competing owner updates, and independent creator billing. Focused
state and workflow recovery tests pass (174.248s, without race detection).

Gateway logging (9.416s), managed PostgreSQL cutover schema coverage (8.106s),
scheduler workflow and application-standard regressions (30.630s), and log
retention (15.560s) pass. Retention preserves the account-plan fallback for
missing apps and the company plan after creator erasure. Nine runtime journal
tables and seven fencing columns are explicitly classified in the clone schema
registry. Project publishing checks human email verification before project
resolution; verification and organization-project HTTP regressions pass (10.235s).

The Go SDK's full tests and `go vet` pass after replacing Go 1.24-only test
contexts with APIs supported by its Go 1.23 module minimum. The storage starter
job keeps every qualification command and now allows 20 minutes after its first
run reached the 15-minute deadline just after the final test command reported
success.

Fresh schema generation, SQL binding generation, migration lint, apply-and-walk,
replay of all seven ownership migrations, and ownership down/up recovery pass.
The migration checks take 25.729s with session-scoped parallel query and index
workers disabled: the local PostgreSQL 16 container's 64 MiB shared-memory mount
was exhausted by the initial run. Final GitHub checks are tracked on
[PR #4882](https://github.com/poyrazK/faas/pull/4882) and must pass before merge.

## Integration with main's internal CI service

Main through `c23392d9a` is integrated, preserving its default-off CI service,
release units, diagnostics and runtime journals. The CI adapter uses the
underlying PostgreSQL pool. All ownership migration definitions retain erasure
blockers for the twenty account-scoped CI metadata tables; the new regression
proves both account-erasure entry points refuse unreviewed CI revisions and
retain the revision.

Fresh canonical schema and SQL generation, deterministic SQL checks, migration
lint against this base, OpenAPI parity with duplicate-key rejection, Node client
generation and build, Python model export parsing, regression-test policy and
whitespace checks pass. The clone registry retains both branches' entries and
covers 848 migrated tables.

PostgreSQL organization ownership, CI replay protection and clone-registry
regressions pass (57.682s). Migration apply-and-walk, replay of all seven ownership
migrations and ownership down/up recovery pass (30.472s). HTTP offboarding,
project ownership, email verification and internal CI route regressions pass
(54.311s). These integration reruns use Go 1.26.9 and PostgreSQL 16 without race
detection; earlier race results above remain scoped to their recorded source.

The fresh GitHub run on `9c67c466d` passed all changed-package lint checks, then
reached the 20-minute job timeout during importer selection. Selection started
after the existing 13-minute importer budget had already expired and was still
fetching Git blobs when canceled. The selector now applies that same budget to
package discovery and selection, terminates its subprocess group on expiry,
and warns that unchanged importers are deferred to the mega tier. Changed-package
lint coverage and the existing vet-batch budget remain intact. All 36 selector
and affected-package regressions pass, including real subprocess timeout,
expired-budget, successful-output and error-propagation cases.

The same run passed required shards 2 and 3, while shard 1 exhausted its
20-minute allowance after cold race compilation and successful gateway, API,
authorization, database and managed PostgreSQL tests. Its scheduler and related
CLI tests had not finished. The PR shard allowance is now 30 minutes; main keeps
40 minutes. Every test command, race setting and per-test timeout is retained.
The lint job allows 60 minutes for broad changes on cold caches while retaining
each selected package's existing lint timeout. It consumes the selector's
`changed` output; full-tree coverage remains in the mega tier. A YAML
semantic comparison verifies that only these job limits change executable
workflow behavior. Fresh CI must qualify the resulting source before merge.

Main through `8548096a3` is integrated, including the application Auth dark
launch and shared-runner base fixes. No migrations or ownership tables changed.
The shared PostgreSQL binding handler retains both the application namespace
and database namespace while preserving the default-off Auth gate. The company
HTTP regression also rejects an Auth binding while that feature is disabled.

Organization PostgreSQL HTTP, manifest, capability, source-binding and email
verification regressions pass (11.701s, without race detection). The full Go
SDK tests and vet pass at its Go 1.23 module minimum. Node generation/build and
OpenAPI duplicate-key/parity checks pass. Four credential-access models in each
of the Node and Python clients are regenerated to match the public schema.
Python Auth and organization PostgreSQL, storage and queue client tests pass
(42 tests); only the four Python models were copied from isolated generation.

## Integration with backend rehearsals

Main through `49ecac58d` is integrated. The seven unreleased ownership migrations
are now `20261018003100001` through `20261018003100007`, after main's rehearsal
migration tail. Their dependent source adapters and recovery fixtures use those
identifiers. The new main migrations do not overwrite the runtime functions
adapted by ownership. Invocation claiming retains the organization queue-policy
lock before resetting a rehearsal binding.

All nine erasure-guard definitions retain the seven new account-scoped rehearsal
tables. A completed PostgreSQL rehearsal fixture blocks both erasure entry
points and retains its evidence. Existing organization/rehearsal state cases
passed; the new fixture case was corrected to supply its required backend
identity, and focused erasure plus clone-catalogue checks pass (10.860s).
The clone registry explicitly classifies eleven rehearsal tables and four new
effect fields, covering 859 migrated tables.

Fresh canonical schema and SQL generation/checks, migration lint, apply-and-walk,
replay of the seven renumbered migrations and ownership down/up recovery pass
(27.578s for the migration suite). OpenAPI duplicate-key/parity checks, Node
generation/build, full Go SDK tests/vet, 42 Python Auth/organization client tests
and 36 CI selector tests pass. These local reruns use Go 1.26.9 and PostgreSQL 16
without race detection. The fresh GitHub checks must pass before merge.

## Integration with internal Product Control

Main through `f63c75848` is integrated, including scoped launches and the
default-off internal Product Control service. Product email uses the underlying
PostgreSQL pool. All nine ownership erasure-guard definitions retain blockers
for its twelve account-scoped tables. A new regression verifies that an
unreviewed Product Control revision blocks both identity-erasure entry points
and preserves the revision after offboarding.

The merged clone registry initially contained eleven identical rehearsal
entries twice. The duplicate entries are removed while retaining every policy
and column classification. Its 877 unique table policies match the migrated
database catalogue exactly. Focused Product Control, rehearsal and CI erasure
tests plus clone-registry coverage pass (11.753s). The preceding broader
organization/Product Control state run passed all cases except the duplicate
registry check, which the focused rerun resolves.

Fresh canonical schema and SQL generation/checks, migration lint, OpenAPI
duplicate-key/parity checks, Node generation/build, full Go SDK tests/vet and
42 Python Auth/organization client tests pass. Migration apply-and-walk,
replay of all seven ownership migrations and ownership down/up recovery pass
(26.101s). Product Control migrations precede the ownership migration range,
so no further renumbering is needed. These local checks use Go 1.26.9 and
PostgreSQL 16 without race detection.

CI comments now describe its actual selected-package lint scope; a YAML
semantic comparison confirms that these comment corrections do not change
workflow behavior. Fresh GitHub checks must qualify this integrated source
before merge. Production deployment and native KVM acceptance remain pending.

## Integration with Data API, callback recovery and developer workflows

Main through `54e5f0150` is integrated, retaining its storage/source hardening,
Data API runtime controls, durable callback recovery and CLI workflow updates.
The SQL generator checks retain both ownership packages and main's new
connection-capacity package. The storage starter retains main's 30-minute
allowance and every qualification command. Node clients are regenerated from
the merged public schema, preserving both API surfaces.

The two new PostgreSQL evidence tables precede the ownership migration range
and introduce no replacement runtime functions. All nine erasure-guard
definitions retain their account references. Connection-capacity qualification
and Data API runtime-connection evidence remain unreviewed company surfaces;
identity erasure refuses to discard those records. The new regression verifies
both erasure entry points preserve connection-capacity evidence even after its
database reaches the deleted state.

Existing organization and clone-registry cases pass in the broader state run.
Its new evidence fixture was corrected to include the required database deletion
timestamp; focused unreviewed-resource erasure and clone-catalogue checks then
pass (12.116s). The registry covers all 879 migrated tables with unique policies.
Migration apply-and-walk, replay of all seven ownership migrations and ownership
down/up recovery pass (26.579s). Fresh schema/SQL generation and deterministic
SQL checks, migration lint, OpenAPI duplicate-key/parity checks, Node
generation/build, full Go SDK tests/vet and 42 Python Auth/organization client
tests pass. These local reruns use Go 1.26.9 and PostgreSQL 16 without race
detection; fresh PR checks must qualify the integrated source before merge.

The seven PR workflows on the preceding `5bba14a8f` source passed with 24
successful jobs and one intentional image-matrix skip. The actual main branch
had advanced while the PR's base-SHA field remained stale. Subsequent
mergeability checks must inspect the current branch ref directly. Production
deployment and native KVM acceptance remain pending.

## Integration with MCP readiness and operations

Main through `637ef59e8` is integrated. Both sides' SQL query sets are retained
and generated bindings are rebuilt. Eight MCP restart fixtures use the
underlying PostgreSQL pool required by the existing store constructor. Main's
parallel starter qualification and trusted main race-cache restore families
are retained, together with every qualification command.

The twenty-three new MCP migrations precede the ownership range and do not
replace its runtime functions. All nine erasure-guard definitions retain the
four new account-linked MCP incident, maintenance and operator-action tables.
A new regression verifies that both identity-erasure entry points preserve an
unreviewed maintenance window after offboarding.

The clone registry explicitly classifies seventeen new MCP tables and six
additional alert fields. Configuration and customer-data records retain their
isolation blockers; deployment observations and cursors retain reset boundaries.
The registry's 896 unique policies match the migrated table and column inventory.
Existing organization state cases and the new MCP erasure case pass in the
broader run. Following registry-category corrections, focused unreviewed-resource
erasure and clone-schema checks pass (12.489s), including the new MCP blockers.

Fresh canonical schema/SQL generation and deterministic SQL checks, migration
lint and OpenAPI duplicate-key/parity checks pass. Migration apply-and-walk,
replay of all seven ownership migrations and ownership down/up recovery pass
(25.876s). Full Go SDK tests/vet, Node generation/build and full Python generation
with the pinned 0.29.0 generator pass; 61 Python Auth, organization and MCP
readiness client tests pass. These local reruns use Go 1.26.9 and PostgreSQL 16
without race detection. Fresh PR checks must qualify the integrated source
before merge; production deployment and native KVM acceptance remain pending.

## Concurrent migration-lock regression

CI exposed a snapshot dependency between waiting fleet migrators and MCP's
nontransactional concurrent index build. A blocking advisory-lock query retained
its statement snapshot while the lock owner waited for that snapshot to finish.
The existing three-migrator regression reproduced the index lock timeout locally.

Migration-lock acquisition now polls `pg_try_advisory_lock` on the same pinned
direct-pool session and key, completing each unsuccessful query before waiting.
Acquisition and unlock errors close an uncertain lock-owning session before
returning its pool slot. Cancellation therefore cannot strand an advisory lock
on an idle pooled connection. Serialization and migration SQL are unchanged.

Two PostgreSQL regressions prove that contenders permit a concurrent index build
and that cancellation during release does not block a separate pool. Both failed
against the previous helper. After the fix, these tests and the existing
three-migrator regression pass with race detection (12.889s); the entire database
package also passes with race detection (79.838s). The configured database-package
linter reports zero issues.
