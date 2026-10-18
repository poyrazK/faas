# ADR: Python guarded budget-adoption parity

- Status: Accepted
- Date: 2026-10-17

## Context

Go and Node can attach finite v2 budgets to existing unbound/v1 acknowledgment
backlog while retaining historical command evidence. Python needs the same
operator command without losing native int64 generations, exact microseconds
or its shielded, joined cancellation and connection ownership model.

## Decision

Add frozen/slotted budget-adoption options and receipts, an explicit precise
candidate read and an async adoption command. Require exact options/budget/policy
types and native integer bounds; reject booleans/subclasses, v2 expectations and
v1 retiming before I/O. None expected policy requires unbound. Complete v1
metadata permits None/native-zero initial budget and must preserve timing. Copy
normalized values into a detached request, retaining full int64 generations and
canonical microsecond UTC timestamps without floating point.

Package the exact shared ledger DDL through schema generation, Python wrapper
preservation, wheel/sdist inclusion and root exports. Match Go/Node's fourteen
ordered digest fields, command/invocation lock namespaces and SQL guards. Require
exact timestamp/count/policy, active unbound/v1 work, no live claim, no newer
invocation generation/attempt and a total above consumed claims. Change only five
policy/grant columns plus UpdatedAt, preserving callback/state/count/due time and
expired claim metadata. Adopted snapshots permit zero consumed claims and an
expired claim, with a validator separate from resume.

Atomically retain adopted/conflict/not-found outcomes. Exact original retries
read historical evidence before current rows and finish without COMMIT. Changed
valid input under a retained UUID conflicts without a receipt/overwrite. Validate
new/replayed snapshots against the original normalized request. Reuse the existing
command transaction and owned/shielded public boundary: close cursors before
COMMIT, join rollback/close, preserve primary commit uncertainty and do not
checkpoint cancellation after confirmed COMMIT/ROLLBACK. Unknown COMMIT returns
no receipt, closes the connection and requires unchanged retry on a fresh one.
Existing v1 adoption, resume and recovery implementations stay independent.

## Consequences

All three SDKs expose budget adoption. Go/Python retain full int64 generation
interoperability; participation by Node uses its safe range. Applications install
the optional ledger beside the budget-upgraded intent table; other helpers do
not acquire a new schema dependency. Applications own authorization, durable
command persistence, receipt retention, scheduling and bounded driver/cleanup
lifetimes. No callback, business work, renewal, recovery capability, count reset,
ingestion-job acknowledgment or pruning is introduced. Producer replay options
must account for explicit old v1 staging conflicting after an intentional upgrade.
See the [Python command and replay contract](../event-acknowledgment-budget-adoption.md#python-selection-adoption-and-replay).

## Validation

Python 3.11 AST syntax and Ruff passed. Isolated regeneration excluding tests
produced 6528 byte-identical Python/schema/type-marker files. SQLC statically
compiled 24 unique combined statements against fresh, upgraded-v1 and pre-policy
upgrade paths, with placeholder/parameter counts checked at all 35 call sites.
Static inspection matched the exact mutation/parameter semantics and fourteen
request fields/domain across all three SDKs, ledger bytes, replay ordering and
source DDL bindings. Wheel/sdist helper/schema/root-export bytes match source.
Shared schema parity, ADR uniqueness, documentation routes/links and whitespace
checks passed. No tests/assertions were added or changed, and no local tests or
SDK helper smoke calls ran. Hosted behavioral qualification remains pending for
mixed SDK adoption/replay, full int64 interop, concurrent claims/staging, expired/
live claims, cancellation, unknown commit, cleanup failure and supersession.
