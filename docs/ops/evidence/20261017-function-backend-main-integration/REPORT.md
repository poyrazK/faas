# Function backend integration with current main

This review increment integrates main at `5f10b1f27616c7543680df4c31b79018312911e1`.
Later integration checks are recorded below with their own source revision.
It preserves the shipped `PostgresPoolBinding` API, its secret-adoption and
starter checkpoint behavior, and the generic migration source journal. The
function decisions are [ADR-20261018005023](../../../adr/20261018005023-function-background-completion-protocol.md)
and ADR-20261018030610 through [ADR-20261018030616](../../../adr/20261018030616-function-migration-scheduler-restart.md).
The existing cancellation decision remains ADR-1540.

## Integration contracts

- The resource-aware pool is an additive SDK export. The shipped database
  adapter also registers its checkpoint/restore callbacks in qualified function
  workers, before its factory can create a pool. Registration survives a timed
  out close until actual pool retirement. Ordinary HTTP applications keep their
  explicit lifecycle and atomic secret-adoption behavior.
- Generic and qualified function migration sources use their own durable
  authorization paths. A pending generic capture can still recover through its
  generation-bound journal; a qualified function requires its resource protocol
  and measured permissions. Neither path falls back to the other's physical
  operations.
- Both migration protocols share pending RAM/CPU/vCPU accounting. A committed
  function source remains charged to its original node until cleanup is
  acknowledged. Scheduler projections and service-capacity protection include
  those claims, and pending ownership blocks node completion.
- Three new, unmerged migrations have IDs after the integrated main schema.
  Destination accounting and its shared projections install in one transaction.
  Existing main migrations retain their published identities and contents.
- The managed PostgreSQL acceptance contract is version 4: main's Node/Python
  starter matrix plus the Node frozen-thaw witness, twenty mandatory phases.
  Older reports cannot certify the expanded contract.

## Local verification

- The SDK packed-runtime import/typecheck check passed. Both pool implementations'
  TypeScript builds passed. The Node REST starter passed 29 tests and the secret
  reload starter passed 8, including the new registration/retirement regressions.
- Fresh PostgreSQL migration apply passed. Reapplying the three new migrations
  preserved the schema. Their reverse down/forward up cycle also restored the
  identical schema. The canonical schema and source digest were regenerated.
- SQLC regeneration/drift, protobuf regeneration, workflow actionlint,
  documentation links and live-docs fixtures, source-policy checks, ADR number
  uniqueness, and migration expand/contract checks passed.
- The ordinary ownership/migration race run passed all 144 state cases, including
  memory/PostgreSQL shared-capacity and retained-source regressions. Scheduler
  and VM-manager cases passed. It exposed a generic uncertain-capture RPC
  integration error; preserving that protocol's generation-bound recovery fixed
  it, and the complete focused source-recovery race suite passed afterward.
- Qualification-tagged simulations executed all 39 cases intentionally gated
  out of the ordinary build. They exposed a separate source-preflight routing
  error; the corrected RPC suite passed for both protocols. These tests use
  simulated host boundaries and supply no native execution evidence.

The complete runner/TLS PostgreSQL race rerun passed without skips, including
both real-SQL lifecycle cases. Its fixtures now copy the complete shipped SDK
runtime import closure. The broader runner profiles, worker-pool, rootfs,
guest-init, template and witness suites also passed their executed cases; existing
environment-prerequisite skips are recorded in the portable result summary.
Hosted PR checks were pending when this record was prepared. Final hosted results belong to
[PR #4883](https://github.com/poyrazK/faas/pull/4883). Earlier increment reports
retain their validation-time source names and repository state; their ADR links
now resolve to the reconciled decisions.

## Post-ready checks and CI correction

At feature head `51d97f9e9b26fec22d0b675736cdb1fb06bff877`, scoped
qualification-tagged lint reported zero issues. The race-enabled native E2E
binary compiled with `metal,function_background_native`; its SHA-256 was
`ff5a96b0486c92864ef60220ba66ff62d1100147b8e6ba83fa47ffe4df3a36f7`.
Compilation supplies no native execution qualification.

The first hosted Data API acceptance run failed in the existing support-audit
fixture with PostgreSQL `57P01`. HTTP shutdown starts asynchronous audit-pool
cleanup, and the fixture could force-drop its database while a client remained
checked out. A 150 ms pause after pool checkout, injected into a temporary copy
of the audit runtime, reproduced the same uncaught termination on Node 22.
Teardown now closes HTTP services before database disposal and asserts that
all of the fixture's audit-login connections have retired. The corrected fixture
passed three consecutive runs with that controlled pause, then both SQL tests
passed with the unmodified runtime on Node 22 and Node 24. Hosted CI on the
corrected feature head still must pass before merge.

## Integration with the internal CI release

Main subsequently advanced through
`c23392d9a7969d9d4beb639d888edf241b650c26`, including MCP diagnostics, the
retained-capture drift fix, and the default-off internal CI service. The function
decisions now use ADR-1658 through ADR-1665, leaving the published CI decisions
at their existing numbers.

The combined schema was regenerated from a fresh owned database: 77,227 lines,
including the internal CI migrations and all three function migrations. Function
migration replay and reverse down/forward up preserved the identical combined
schema. SQLC and protobuf drift checks, ADR uniqueness, documentation links/live
fixtures, and the Node SDK build passed. The affected state and scheduler race
run passed 104 and 72 tests/subtests respectively, with no skips or failures. It
includes both function handoff/capacity coverage and the internal-CI scheduler
boundary and snapshot-drift regressions.

## Explicit starter SQL gate

The hosted shard-1 starter gate exposed fixture calls left over from the earlier
pool adapter: two asynchronous runtime creations lacked `await`, restore options
were dropped, and a diagnostic assertion used an old field. The fixture now
awaits startup, forwards its deadline, checks the shipped checkpointed state and
zero active requests, and verifies that both a retained runtime and a fresh
restore reject a retired SQL login. It still observes zero server-side sessions
before capture and an exact generation increment on restore.

The full `TestPostgresStarterSQLRecovery` gate, explicitly enabled with
`GREGALE_POSTGRES_STARTER_TEST=1`, passed against owned TLS/SCRAM PostgreSQL on
Node 22 and Node 24. Each run executed fourteen SQL scenarios plus the parent
test: fifteen passes, zero skips and zero failures. These are portable SQL
lifecycle results; no native VM capture or restore is qualified by them.

## Integration with the Auth and rehearsal releases

The branch also integrates main through
`49ecac58dad13a8d0ee27f0e8af5206d4d7408dc`. The function destination replay
fence stays ahead of rehearsal network setup. Activity retains both managed
dispatch identity and independent execution ownership; a single held dispatch
cannot authorize suspension while separate function execution remains. Focused
regressions cover both interactions.

The three unmerged function migrations now follow the published rehearsal
migrations at versions `20261018003150915`, `20261018003150916` and
`20261018003150917`. Published main migrations retain their bytes and identities.
Fresh combined schema generation produced 78,875 lines, and replay/down-up
preserved that schema. SQLC and protobuf drift, SDK build, ADR uniqueness,
documentation/live fixtures and the full activity race suite passed. The larger
qualification-tagged simulated-host race run and native-binary compilation were
still pending when this increment was recorded; the PR tracks completion before
merge. Function native activation remains disabled.

## Completed local checks and management SDK contract

The combined qualification-tagged simulated-host race run subsequently passed
481 tests/subtests with zero skips or failures. The native E2E binary compiled
from the integrated source, with SHA-256
`59a0c6b6764b6dd7d0d77225ef3f51db9269f5b877948f6fcf42e33bdcd072ef`.
No native test body was executed.

Hosted management-client regeneration also found stale PostgreSQL access enums
in the Node and Python generated SDKs. The published Auth dark-launch change
deliberately removed `auth` from the public binding schema. Regeneration now
matches that schema's four access modes. The published spec and feature flags
retain their existing behavior. The Node SDK build and Python compilation pass.

## Integration with the Product Control release

Main advanced through `f63c75848c99002fe379db8f3f1c17e1777fab63` while hosted checks were running.
The merge preserves the complete default-off Product Control SQLC configuration
and status entries alongside all three function query sources. A fresh owned
PostgreSQL database produced an 80,013-line schema; function migration replay
and reverse down/forward up preserved the identical combined schema. Published
main migrations retain their bytes and identities.

Main also published deployment response checks as ADR-1658. Only the unpublished
function background-completion decision moves to ADR-20261018005023; function
ADRs 1659–1665 and the published response-check decision retain their identities.
The ADR uniqueness gate and documentation/live HTTP fixture checks pass.

SQLC generation/drift, both management SDK generators with an empty resulting
diff, the Node SDK build and Python compilation pass. The qualification-tagged
state/scheduler race run passes 298 and 48 tests/subtests respectively,
with zero skips or failures. It exercises the function handoff contracts and
Product Control state on the combined schema using simulated host boundaries.

The previously recorded native binary hash belongs to the prior rehearsal
integration. Fresh-head hosted checks, including guarded native compilation,
must finish before merge. No native execution or production activation is
qualified by this integration.

## Hosted SQL fixture and clone inventory corrections

The function portable workflow now opts into the existing migrated-template
fixture mode. The full migration history runs once, and each state test receives
its own database clone. Migration-focused fixtures using `pgtest.Open` keep their
empty-schema behavior. The test timeout remains three minutes.

The ordinary function SQL selection plus registry and migrated-schema coverage
passed 76 race tests/subtests without skips or failures within that deadline.
The binary was compiled separately with a 5 GiB memory limit, then executed with
CI's 2 GiB limit, GOGC 10 and two logical processors. The migrated-schema test
still rejects unknown columns/tables before reserving an operation.

Both function handoff journals are explicitly operational in the clone inventory.
Capture permissions, leases, destination readiness and physical ownership stay
with the original instance; environment cloning cannot copy that authority.
The exact managed-PostgreSQL cutover/clone-exclusion and live-authority downgrade
regression passes in 8.43 seconds. Workflow actionlint passes. No schema migration,
production activation or native execution qualification is introduced.

## Full-package lint and bounded compilation

The old private `migrationTracker.reserve` forwarder has no callers after
source-aware reservations adopted `reserveSource`; it is removed. Full-package
lint passes without filtering findings to changed lines in both ordinary and
qualified builds. The qualified check reports only nonfatal fact-cache write
warnings from the managed read-only home cache; analysis completes with zero
issues. Subsequent local commands select a writable workspace cache.

Existing migration RPC race coverage passes 59 tests/subtests in ordinary builds
and 65 in qualification builds. The ordinary qualification-only recovery guard
skips as designed and executes successfully in the qualified run. These are
simulated-host results, not native execution evidence.

The hosted shared-worker job completed every test command successfully, then
exhausted its 45-minute job allocation 28 seconds into native compilation. Its
non-race image-service build alone consumed about eleven minutes for a test run
of 0.042 seconds. Compilation now runs separately, serially, with a 5 GiB Go
memory target and GOGC 50. Executed tests retain the 2 GiB target, GOGC 10,
two logical processors, original selections and deadlines. Image lifecycle tests
now use race instrumentation and the already-built race dependency graph. The
job budget and read-only CI cache policy are unchanged.

The multi-package `go test -c` output-directory path produced all three expected
ELF binaries without executing test bodies. Both selected image lifecycle tests
then passed with race instrumentation under the unchanged CI runtime settings
and three-minute deadline. Workflow actionlint passes.

The guarded native suite compiles from source base `758198bc429b1a568abd0b7f009f8cc9c4b7194a`
plus the recorded tracker-removal patch, using the existing managed dependency
cache and the new compiler settings. Its binary SHA-256 is
`36393c32b0ff0f9b5d58043fe07d9ea86f7702657f1e57d55915875f3fe847d0`.
This records compilation only. No native test body or production activation is
qualified; hosted CI must still pass on the newly published head.

## Integration with Data API and event recovery releases

Main through `ee0cc7135dac4149b1c905cf36e628088ab279f9` adds connection-capacity
observations, recoverable Data API workflows and durable event retry/recovery
controls. All sixteen SQLC source blocks and the published query list are
retained alongside the three function sources. Fresh combined schema generation
produced 80,157 lines; function migration replay and reverse down/forward up
preserved the identical schema. SQLC generation/drift and documentation/live
fixture checks pass.

The published Data API decisions retain ADR-1660 through ADR-1678 byte-for-byte.
The remaining unpublished function decisions use timestamp IDs
20261018030610 through 20261018030616; background completion keeps
ADR-20261018005023. Only function-owned added-line references and live link
targets change. Historical evidence numbers and hashes retain their original
meaning. ADR uniqueness holds the existing 164-duplicate baseline.

Both SDK generators, the Node build, Python compilation and canonical runtime
helper drift checks pass. Generated access enums follow current main's published
five-mode OpenAPI schema, including `auth`; the earlier four-mode dark-launch
record above describes its earlier source revision. The published spec and
activation flags are preserved.

Qualified race contracts on the combined schema pass 76 state, 44 managed
PostgreSQL and 6 API tests/subtests with zero skips or failures. Test binaries
compile separately, then execute with CI's 2 GiB target, GOGC 10, two processors
and three-minute deadline. Local external state-test DWARF is disabled to fit
the managed disk; race instrumentation and assertions remain enabled. An initial
compile reached workspace capacity before tests ran; generated archive caches
were reclaimed and the complete contract run passed. No native test body or
production activation is qualified by these checks.

## Integration with MCP readiness release

Main through `637ef59e883a26efdc8de554b039e15e99a8fcd8` adds MCP starter
parity, monitoring and readiness operations. The merge retains all thirty
published MCP timestamp ADRs and both OpenAPI copies byte-for-byte. Only the
three function migrations differ from published main; their IDs remain after
the newly integrated MCP migrations.

Fresh schema generation reports 81,257 lines. Replaying the three function
migrations and reversing down/forward up preserve the identical combined
schema. SQLC generation/drift, both SDK generators, TypeScript compilation,
Python compilation and canonical helper drift checks pass. Generated management
SDK trees match published main. ADR uniqueness preserves the 164-duplicate
baseline across 1,767 decisions, and documentation links and their four local
fixtures pass.

The [hosted function gate for source `ef08649b068014911061c58f1d24f51611b810cb`](https://github.com/poyrazK/faas/actions/runs/39094030717/job/117576589186)
passed in twenty-four minutes, including runner/real SQL races, execution
ownership checks and guarded native compilation. This confirms the revised
compiler phase within the unchanged job budget for that source. All applicable
hosted checks must pass again on the new combined head before merge. Native
compilation supplies no physical execution or production activation evidence.

The subsequent combined-source race check identified seventeen unregistered MCP
readiness tables and six new alert columns in the live clone inventory. The
registry now names every exact column: seven policy/maintenance tables retain
configuration isolation blockers, and ten deployment/incident/delivery tables
remain operational source state. Alert rules and deliveries retain their prior
configuration and operational classifications. The existing regression contract
checks these boundaries and all seven isolation blockers; unknown tables and
columns must still fail before an operation is reserved. The corrected source
`2884470d07efc858a90d05ec5f0a4b4767d39ce3` passes 202 selected race contracts:
76 state, 44 managed PostgreSQL, 6 API, 75 simulated host/function/MCP and one
log-buffer test/subtest, with zero skips or failures. Compilation took 553
seconds with a 5 GiB/GOGC 50 target; executed tests retained 2 GiB/GOGC 10, two
processors and three-minute deadlines. No native test body ran.

## Light shard compilation budget

On source `2884470d07efc858a90d05ec5f0a4b4767d39ce3`, light shard 3 was
cancelled after 20 minutes and 16 seconds, at its configured twenty-minute job
limit. Binding harness preparation took five minutes and eleven seconds, while
the four actual binding package test runs each took about one second. The
main race compilation pass took another six minutes and twenty-nine seconds.
Its full test run was cancelled seven minutes and twenty-seven seconds into
the unchanged ten-minute test allowance. No completed assertion failure was
reported before cancellation; the run is not counted as passing.

The binding harness now precompiles its four binaries without executing test
bodies, using a 5 GiB/GOGC 50 target. The main race preparation pass also compiles
serially with those compiler targets. Executed binding and main tests retain
their existing 2 GiB/GOGC 10 bounds, selections, concurrency and deadlines; the
job limit remains twenty minutes. Workflow actionlint and all fifteen package
selector tests pass. The full affected shard selection and fresh-head hosted
CI must verify the correction. This is a compiler-budget correction, not a
transient-infrastructure retry or native qualification.

## Scheduler SQL fixture isolation

With CI's PostgreSQL lock capacity and process umask, the broader local run
reproduced two remaining failures in scheduler fixtures. Both replayed
`20261017181315007_mcp_readiness_incident_timeline.sql` in separate schemas of
one database while other tests held snapshots; its concurrent index build hit
the unchanged five-second lock timeout.

The scheduler entrypoint/cluster-key fixtures and operator trace-completeness
fixture now use the existing `OpenMigrated` helper. CI's existing template
opt-in gives every test a private database cloned from the complete migrated
schema. `db.MigrateUp` remains in the helpers so the non-template path still
migrates a fresh isolated schema. Existing skip opt-outs, assertions, runtime
settings and deadlines are retained. Migration-focused fixtures are unchanged.

The full ordinary race suites pass 129 entrypoint and 3,149 scheduler
tests/subtests with zero failures, using 2 GiB/GOGC 10, two processors, package
parallelism four and ten-minute deadlines. Three existing qualification/path
skips remain recorded in the machine-readable evidence. The operator metric
case also passes with the template opt-in disabled, including fresh schema
migration. The fixture patch SHA-256 is
`0f248c77015620c954a661678a4b175d023284488597dfc031db4e3e979aab89`
against source `bea394b506b0f37deb7c0e855184ee76cfee29ef`.

The broader local ordinary shard is not counted as a complete pass: the managed
container's temporary filesystem/process umask and TEST-NET probe behavior
differ from the hosted runner. The hosted gate retains the entire affected
shard and must pass on the new exact head. No native filesystem or KVM
acceptance is supplied by these fixture checks.

## Published observability and live-instance integration (2026-10-18)

The fixture correction is retained while integrating published main through
`2a4c0d1fca2b7b01bb7bda206b822a135ce45d1e`, including live-instance filtering,
the observability schema and MCP clone classifications. Validation used the
merged source tree `6cd0bd0a6f9dde98041065fab72787c3596bb70e` before this
evidence update. The interrupted pre-restart validation is not counted.

The canonical schema was regenerated from a fresh disposable PostgreSQL 16
database, with migration source digest
`e20c0fd4bc038473c31571eb2a1d6b2a13aa781b9b7ffe866b33cacd3c601326`.
All sixteen SQLC generation blocks and three function query sources remain;
SQLC generation and drift checks pass. The clone registry preserves all 901
published main classifications and column sets, plus the two operational
function journals, without duplicate MCP entries. Both management SDKs
regenerate without changes to the merged generated trees; Node builds,
Python compilation and canonical helper drift checks pass.

Fresh full ordinary race execution passes 129 scheduler-entrypoint and 3,149
scheduler tests/subtests, with the same three existing skips and zero failures.
Qualified state and scheduler selections pass 79 and 28 tests/subtests; the
additional live PostgreSQL schema contract passes five. All 112 qualified
checks execute without skips or failures. The MemStore and PgStore committed
source-cleanup cases retain source capacity until cleanup acknowledgment;
the live-state enumeration and unknown-schema-before-reservation cases pass.

Compilation remains serial with 5 GiB/GOGC 50. Executed tests retain 2 GiB,
GOGC 10, two processors and unchanged ten-minute ordinary/three-minute
qualified deadlines. The private TLS fixture uses 512 locks per transaction
and 1 GiB shared memory. ADR uniqueness, documentation links and four local
HTTP documentation fixtures pass; the three function migrations pass the
expand/contract check against the integrated main. New exact-head hosted CI
and the guarded native compile remain required before merge. No physical
execution or production activation is supplied by these checks.

## Durable listener compatibility (2026-10-18)

Published main through `744a82c005887d75c01b9c0210560fade3ca45a7` adds
inherited listeners and bounded stream draining. The merged source tree
`31d473f5aea3c73403b32b2f7be8658006f5e786` passes full scheduler-entrypoint,
wire and scheduler-client race suites: 130, 500 and 182 tests/subtests,
respectively, with three existing helper/path skips and zero failures.
Compilation uses 5 GiB/GOGC 50; execution uses 2 GiB/GOGC 10, two processors
and three-minute package deadlines.

The main integration exposed two different ADR-1580 decisions. The older
internal CI admission decision and duplicate-number baseline are preserved.
The newer daemon restart decision uses ADR-20261018074719; only its citations
introduced by the daemon restart commit are updated. No restart behavior is
changed by that correction. ADR uniqueness passes with the existing 164
baseline entries. Generation, environment-contract and documentation checks
pass, including seven listener-template and four HTTP documentation fixtures.

## Selective repair contract integration (2026-10-18)

Published main through `fb63fcc029d8865cb662a08b18bcbae8696a2369` introduces
data-repair protobufs, two migrations and native lifecycle paths. The merge
preserves both the function destination/wake identity and the data-repair-only
marker in cold-start instances. All published SQLC sources, including the two
repair query files, are retained alongside the three function query files.

The schema is regenerated from a clean disposable database with source digest
`750a73093169a71c79989608458307641d7f2b69d68e66b7caa2df53b19e2ed7`.
Protobufs are regenerated and normalized; all sixteen SQLC blocks regenerate
and pass drift checks. Both OpenAPI copies and generated management SDK trees
match published main. Node build, Python compilation and helper drift checks
pass. Every published clone policy is retained, with only the two operational
function journals added.

Merged source tree `9fc4217c110f34408060cc3b00a1ec0af17cdf44` passes 80
qualified portable host tests/subtests and 34 scheduler tests/subtests covering
function lifecycle and data-repair retirement/RPC contracts. All 114 execute
without skips or failures. Serial compilation uses 5 GiB/GOGC 50; execution
retains 2 GiB/GOGC 10, two processors and three-minute deadlines. The earlier
full scheduler/state evidence remains associated with its recorded source.
Fresh-head hosted state checks and guarded native compilation remain required.

Generation, environment-contract and documentation checks pass; ADR uniqueness
passes with 1,788 decisions and the unchanged 164-entry duplicate baseline.
Only the three function migrations differ from the integrated main and pass
the expand/contract check. Native activation and physical acceptance remain
pending.

## Generation handoff and release contract compatibility (2026-10-18)

Main through `60e81018c0a60f2040dfd6d708d7a5f0d1d97f79` adds generation
handoffs and per-unit release validation. Its new daemon restart citations
follow ADR-20261018074719. The older CI decision and duplicate baseline remain
intact. Function host/scheduler sources, schema, SQLC configuration, protobufs,
OpenAPI and SDK trees match the previously validated `e13d5b7a9` source.

Merged tree `2cba47278ca70c098b385f0f205395ebc4bb1a22` passes seven handoff
race tests and four generation-slot tests. Two direct helper entrypoints skip;
the parent tests execute their subprocess paths. Daemon-unit race tests pass
148 tests/subtests; a follow-up using the installed Python renderer passes 23
more, with two distinct optional Ansible-related tests remaining unavailable.
The eight edge-rollout cases and both hermetic unit-deployment/release-verifier
shell suites pass. Their SSH, SCP and SLO execution are stubbed; no deploy runs.

The first local handoff attempt correctly refused to create a successor while
build artifacts/cache consumed about 13 GiB of the 16 GiB cgroup. Completed
task-owned test binaries were removed and clean generated Go archive pages were
reclaimed, reducing actual usage to about 4 GiB. The unchanged production
headroom guard and the same handoff tests then pass. No memory limit, refusal
threshold, assertion or deadline was changed. Generation, environment-contract
and documentation checks pass, including four HTTP fixtures. Fresh hosted checks
on the published exact head remain required.

## Mail-service schema coexistence (2026-10-18)

Main `53116be8b3417a7124b46fcc2849e567066cb853` adds the default-off mail
service and 34 migrations. A clean disposable schema replay produces migration
digest `5043510cb3989f8ae58d42d2f5086ef2e188554e6d0928fd8955b883729f1a70`.
All eighteen SQLC blocks and every published query source are retained alongside
the three function sources; generation and drift checks pass. All 945 published
clone policies are preserved, plus the two operational function journals.

Function host/scheduler/query sources, protobufs, OpenAPI and SDKs match the
validated `93e20dda4` tree. Generated daemon, environment and documentation checks
pass, including four HTTP fixtures. The new mail decisions collided with older
static-hosting and data-repair IDs; only the two mail decisions and their six
documentation citations move to ADR-20261018085140/20261018085141. Older decisions,
published migration bytes and the 164-entry duplicate baseline remain intact.
ADR uniqueness and the three function migration expand/contract checks pass.
Fresh exact-head hosted checks remain required; native activation remains off.

## Durable worker recovery coexistence (2026-10-18)

Main `2243dce395256150b5d4841206057d896d2dd418` adds durable worker recovery,
admission explanations and newer migration versions. Only the three unpublished
function migrations are reallocated with the repository's `migration-new`
allocator, after the published worker migrations:

- `20261018095849457_function_migration_sources.sql`
- `20261018095850003_function_migration_admission.sql`
- `20261018095850501_function_migration_destination.sql`

Earlier evidence retains the versions used for those earlier executions.
Published migration bytes remain intact. A clean schema replay and an upgrade
from worker version `20261018013954350`, including a down/up of exactly the
three function migrations, produce byte-identical canonical schemas. The new
source digest is
`58bcf3107ece49813545201ec9c2f7e465350b33f47d8d4193bfecb645e9a0ee`.
All eighteen SQLC blocks and published worker query sources are retained;
generation/drift checks pass. The 946 published clone policies and two
operational function journals remain complete and distinct.

Merged source tree `9090f5bfc1cced6337c32642336f06c4eed08aa0` passes 54
qualified scheduler function/worker recovery and admission tests/subtests with
zero skips or failures. Compilation uses 5 GiB/GOGC 50; execution retains
2 GiB/GOGC 10, two processors and the three-minute deadline. Both SDK generators
match main's worker OpenAPI, TypeScript builds, Python compiles, and helper
drift checks pass. Generated daemon, environment, ADR, migration and documentation
checks pass, including four HTTP fixtures. Fresh hosted exact-head checks and
native compilation remain required; physical acceptance and activation stay
pending.

## Migration release compatibility declarations (2026-10-18)

Hosted job `117648546009` on source head
`73220ee13d44099adfb73e430417244dc8efbb7a` rejected the three function
migrations because main's newer release policy requires explicit compatibility
declarations. Main through `6e2e9441958137100584776cbc95ff5542e06df6` is
integrated. The source journal and admission migrations declare `expand`;
the destination migration declares `contract` because it changes existing
publication, runtime and capacity guards. Only a full release may apply that
destination migration; the per-unit lane must refuse it.

Only compatibility comments change the three SQL sources. The canonical schema
remains byte-identical with SHA-256
`b5a03e0b85235a93c9893c241df260e166ea11ff13530051b21a6396b364a214`.
The refreshed migration-source digest is
`4f9c05d9d676126d0c4b66f6a53aa68ec483db74dae31ff1ce28f1659487f465`.
SQLC drift validation and the exact migration compatibility check pass.
All 51 compatibility-policy tests, 25 affected-unit tests, 19 deployment-plan
tests and both hermetic deployment/release-verifier shell suites pass. The
first affected-unit attempt used the older default Go toolchain and failed
before executing its end-to-end cases; the rerun uses the repository's pinned
Go toolchain. Shell fixtures stub remote execution and do not deploy.

Before publication, main `88c804c71e75bd69691684a6a736b0306f7b6565`
published its own ADR disambiguation. The merge adopts main's mail decision
names `2026101501`/`2026101601` and internal-CI recovery decision `1592`;
the earlier timestamp mail names remain only in historical evidence. The
static-hosting and data-repair references in the limit and environment contract
are restored to their original `20261015`/`20261016` decisions. The daemon
restart decision retains `20261018074719`. No published migration is modified.
ADR uniqueness, generated daemon configuration and environment contracts pass.
Documentation links and all four local HTTP documentation fixtures pass.

Function host/scheduler sources, protobufs, OpenAPI and SDKs are unchanged from
the preceding worker-integration head. No assertion, policy or resource limit is
weakened. Fresh exact-head hosted checks remain required; physical acceptance
and activation remain pending.

## Release-policy coverage and MCP schema coexistence (2026-10-18)

Hosted job `117654151912` on head
`c68bd0016df42965a0e7654e45ee40aeb3e1393a` found one failed test:
`TestUnitLaneRestartsOnlyPlannedServicesOverTheExpectedRelease` still rejected
any occurrence of `migrate`, including the new policy's guarded migrator
staging. Its replacement assertions require signature verification, signed
manifest equality, every migration's eligibility, the migrator digest and
staging before activation. Full-lane-only operations, compare-and-swap,
service selection, activation gates and rollback checks remain enforced.

Main `3eadd00f38a449e46dc8d10d0c772256e0b557c5` is integrated, including
its MCP review-version migration and both new query sources. A clean replay
in the task-owned disposable database generates an 84,179-line canonical
schema with SHA-256
`dc58d8308173c402e1ea051ff18ddd9de07201c02bd02a5d9ffe66e77f8afc24`
and migration-source digest
`b4779ad555a3a1f8921d17b294fe17dc46360a921930f463e8843ca5b6fa4f06`.
All eighteen SQLC blocks, published query sources and 946 published clone
policies remain intact, alongside the three function query sources and two
operational journals. There are no duplicate clone policies. Both management
SDK generators match main's OpenAPI and generated trees; TypeScript builds,
Python compiles and canonical starter-helper drift checks pass.

Tested source tree before this evidence entry: `4e1d5ab4d798b2ac31b0f77e12112432797c06f5`. All six
release-workflow race tests execute with zero skips or failures. Compilation
uses 5 GiB/GOGC 50; execution retains 2 GiB/GOGC 10, two processors and the
three-minute deadline. All 51 migration-policy, 25 affected-unit and 19
release-plan tests and both hermetic shell suites pass. ADR uniqueness passes
with 1,817 decisions, 1,613 distinct numbers and the unchanged 164-entry
baseline. Generated daemon configuration, environment and documentation checks
pass, including four HTTP fixtures. No remote deployment runs.

The destination migration remains contract-classified and requires a full
release. Function host/scheduler/protobuf sources are unchanged from the prior
head. Hosted worker/TLS SQL tests and guarded native compilation passed on
`c68bd0016`; every applicable hosted check must pass again on the fresh head.
Physical native acceptance and production activation remain pending.

## Rehearsal schema coexistence and source authority (2026-10-18)

Main `a7aa553e2847c5dc265593c3efd2954cb915eb29` is integrated after its
new PostgreSQL rehearsal migration conflicted with the migration digest. The
canonical schema is replayed in the owned disposable database: 84,382 lines,
SHA-256 `b14c4a23e73a79f1ef0d2006d82c7d8e3734d887d52d68ea42ec09cac45e16d4`,
migration-source digest
`37711e5df864ef032913f702bd55590e214651e81c82ed7a77a7f31e08b2480c`.
All nineteen published SQLC blocks are preserved exactly, with only the three
function query sources added; generation and drift checks pass. Published
migration bytes remain unchanged.

The actual migrated inventory exposed one unregistered main table:
`scenario_postgres_faults`. Its fault/recovery receipts bind their original
account, app, run, resource and owner. It is explicitly operational, alongside
other rehearsal receipts, so cloning retains that authority at its source.
All 946 published registry records are intact. With the two function journals
and this rehearsal record, all 949 observed tables and every column match the
registry exactly; there are no duplicate, missing or unknown entries.

Tested source tree before this evidence entry: `18bc1b4f0929cd923b12367a8e55582024453f8e`. Serial race
compilation succeeds in 551.610 seconds at 5 GiB/GOGC 50. All six schema-policy
and real PostgreSQL coverage tests/subtests pass in 21.474 seconds, including
unknown-column, unowned-table, indirect-table and renamed-table refusal before
reservation. All seven release-workflow tests pass in 1.220 seconds, including
main's stricter ban on running migrations outside deployctl. Both suites use
2 GiB/GOGC 10, two processors and three-minute execution deadlines, with zero
skips or failures in the successful executions.

The first SQL attempt could not create its fixture database because the shared
workspace filesystem filled after compilation. With Go idle, only 1,445 old
generated Go archives (4.0 GiB, validated archive headers, older than two hours)
were removed. Modules, tool executables, test binaries and source remain intact.
The same compiled SQL tests then pass; no assertion, timeout or resource limit
changes. The failed infrastructure attempt remains in the portable results.

Both SDKs are regenerated for main's new rehearsal API fields and models;
TypeScript builds, Python compiles and canonical starter-helper drift checks
pass. The two OpenAPI copies retain main's source. Migration compatibility and
all 51 policy tests pass; the destination migration remains contract-classified
and requires a full release. Generated daemon configuration, environment and
documentation checks pass, including four HTTP fixtures. The affected-package
selector passes all 19 tests. ADR uniqueness passes with 1,820 decisions, 1,616
distinct numbers and the unchanged 164-entry duplicate baseline.

Function host/scheduler/protobuf sources are unchanged from `e406b27ed`.
Successful hosted checks on that earlier head are historical: the image workflow
was still pending when main changed, so that head was never declared green.
Every applicable hosted check must pass on the newly published head. Physical
native acceptance and production activation remain pending.

## Scheduler environment-test startup synchronization (2026-10-18)

Hosted shard-one job `117687161440` on head
`dd29f31a0b257bb4268dff11cae74447a21b2598` found
`TestRun_EnvMigrateLiveMaxPerTick_BadValue` returning no validation error.
Its helper canceled startup after a fixed 200 ms, which can precede the
scheduler's environment validation. The helper now waits for either the
validation result or the actual systemd `READY=1` notification. A single
three-second bound covers startup and cancellation. Invalid-value assertions
remain intact; a clean exit before readiness also fails the test. Production
startup, schemas, SDKs and resource/deadline settings are unchanged.

Tested source tree before this evidence entry: `d5599d885f8e5fdf2cb0e5bbd03377c4a56261cf`. Serial race
compilation passes in 22.840 seconds at 5 GiB/GOGC 50. Twenty consecutive
executions of the previously failing case pass in 33.299 seconds without
skips. The complete scheduler command race suite passes 130 tests/subtests
in 34.700 seconds, with one existing skip:
`TestLoadSchedInternalSvcKey_SealedPath` requires root to write the default
host-secret path. That boundary remains unqualified; no host secret is touched.
Execution retains 2 GiB/GOGC 10, two processors and three-minute suite deadlines.

An initial twenty-fold repetition of the entire environment group passed 222
cases before exceeding its aggregate three-minute suite budget. The targeted
stress run and the full suite above provide the successful qualification; the
incomplete group attempt remains recorded in the portable results. Neither
assertions nor deadlines are relaxed. Fresh hosted checks on the newly
published exact head remain required. Native activation remains disabled.

## Node-liveness lease schema coexistence (2026-10-18)

Main `622ea65b973fd6808c8b7f2ac8adc79296e84750` is integrated after its
node-lease migration and SQLC query source caused generated-file conflicts.
All nineteen main SQLC blocks and the node-lease queries are preserved exactly;
only the three function query sources are added. Clean schema replay,
generation and drift checks pass: 84,414 lines, schema SHA-256
`c7a32e9f468add83cd34c576c34000011e0e64052676b37f327690f0a9f07d04`,
migration-source digest
`575aba09fb59a4cac517be1083d75e608139352a17a09b26c9a52fa1da5b4142`.
Published migration bytes remain unchanged; the three function versions already
follow the new node-lease version.

The actual migrated inventory exactly matches all 950 registered tables and
columns, without duplicates or unknown/missing entries. All 947 published
policies are intact, including the operational node lease, alongside the two
function journals and operational rehearsal fault receipts. Node-lease phase
one is off by default and only renews/observes; it introduces no workload
recovery or fencing action.

Tested source tree before this evidence entry: `f8ab19c7e263345bcd4b2d726bb37e0411f15480`. Serial race
compilation with main's recommended `-ldflags=-w` passes in 189.221 seconds at
5 GiB/GOGC 50. Four lease-keeper tests pass without skips in 1.137 seconds.
The full scheduler command suite passes 130 tests/subtests in 28.277 seconds,
with the existing root-only sealed-host-key test skipped. Execution retains
2 GiB/GOGC 10, two processors and three-minute deadlines. The readiness-based
environment-test helper is unchanged and remains covered by the full suite.

Generated daemon configuration, environment and documentation checks pass,
including four HTTP fixtures. All 51 migration-policy tests pass; ADR uniqueness
passes with 1,820 decisions, 1,616 distinct numbers and the unchanged 164-entry
baseline. The destination migration still requires a full release. Function
host/scheduler-engine code, protobufs, both OpenAPI copies and SDK sources are
unchanged from `410c47af5`, so no SDK regeneration is needed for this merge.
Fresh exact-head hosted checks remain required; physical native acceptance and
activation remain pending.

## Current main conflict resolution (2026-10-18)

Main `d3ccdb1d8e03b8515a711ffdcf7c13871256e83d` is integrated. The deployctl
restart loop now uses main's `restartOne` helper with hand-off enabled; that
helper retains the PR's preload, ownership, hand-off/fallback and readiness
steps. PostgreSQL constraint mapping keeps both the organization ownership
error and the function migration conflict. The clone-schema registry keeps
main's latest classifications and columns, plus the two function migration
tables; it has 961 unique policies. Its expected-blocker test also includes
main's MCP incident-event case. SQLC retains every main query source and adds
the three function migration sources. The migration-source digest was
recomputed as `3a39625c798c3b258496ca8031a4b3d1d93a4e071a97a636a635e8ecb7ffd117`.

The daemon restart decision is back at its canonical ADR-1580 path and
references now use 1580; main's internal-CI recovery decision remains ADR-1592.
Historical evidence keeps the earlier revision's ADR number. Main's APID
memory change is retained: its 45% hand-off threshold is unchanged, with a
bounded 60-second wait, 512 MiB unit cap and 256 MiB per-process GOMEMLIMIT.

Tested source tree before this evidence entry: `2727f66f7882af5dbcafae154740b83b54419c78`.
`make SQLC=/workspace/faas-env/task-tools/sqlc sqlc-check` passes. Focused
race tests pass for the memory hand-off wait, deployctl hand-off/restart
selection and APID unit memory contract. ADR uniqueness passes with 1,841
records, 1,637 distinct numbers and the unchanged 164-entry duplicate
baseline; the environment-contract check passes. The canonical schema source
digest test passes, and the docs-links check passes all four fixtures. Main
subsequently advanced to `c746937a74080c71c26e65a0dca89646cbee950b`; that
commit only changes two scheduler tests outside the conflict set. The changed
PostgreSQL clone registry has not been replayed against a local database on
this source tree; fresh hosted checks remain required. Native activation
remains disabled. Hosted lint on the first resolved merge head stopped at its
gofmt step for `pkg/state/statetest/dispatch.go`; its lint/build steps were
skipped. That file is now formatted with the pinned Go formatter. This is a
formatting-only correction; fresh exact-head checks are required.

The next exact-head CI run exposed stale MCP readiness classifications in the
branch's clone-schema test: main now marks deployment overrides as scoped
configuration and six incident/action tables as customer data. The test now
matches those current classifications and asserts that the configuration/data
entries remain blocked until an isolated capture strategy exists. Tested source
tree: `214797bc5a2126bcc27366c69d9bf056c3f2ac04`. The focused
`TestCloneSchemaRegistryNamesConfigurationDataAndResetBoundaries` passes with
the pinned Go toolchain. Fresh hosted checks on the corrected head remain
required.

## Qualification boundary

Production activation remains disabled. This managed container has no supported
root/KVM acceptance fixture. The strict native entry point refuses its root
prerequisite; leakcheck cannot inspect the container's native loop devices. A
scoped `test-metal` build attempt was interrupted to serialize expensive builds.
No native test body, two-host migration, native leak acceptance, or production
release qualification is claimed. The twelve-case native migration matrix and
the remaining service/host/vmmd recovery and transport-failure gates still need
execution on designated physical hosts.

Disposable SQL credentials and databases are test material only. They are not
part of this evidence or a production rollout bundle.
