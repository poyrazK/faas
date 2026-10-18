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
