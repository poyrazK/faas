# Function backend integration with current main

This review increment integrates main at `5f10b1f27616c7543680df4c31b79018312911e1`.
It preserves the shipped `PostgresPoolBinding` API, its secret-adoption and
starter checkpoint behavior, and the generic migration source journal. The
function decisions are [ADR-1571](../../../adr/1571-function-background-completion-protocol.md)
through [ADR-1578](../../../adr/1578-function-migration-scheduler-restart.md).
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
