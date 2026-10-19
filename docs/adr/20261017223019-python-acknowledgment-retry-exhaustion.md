# ADR: Python acknowledgment retry-budget parity

- Status: Accepted
- Date: 2026-10-17

## Context

Go and Node implement v2 application-owned recovery budgets, parked callbacks and
explicit receipt-backed grants. Python needs the same durable contract while
preserving native int64 generations, exact timestamp fences and its shielded,
joined cancellation and connection ownership model.

## Decision

Add a separate frozen/slotted RetryBudget with required positive int32 total and
optional timing policy. All six durable staging paths accept a `retry_budget`
keyword, mutually exclusive with `retry_policy`, copied/validated before I/O.
Keep omitted staging unbound and v1/fallback policies unlimited. Strictly decode
v2 initial totals/current grants and compare original immutable bindings during
repeated staging, so resume cannot be undone by a duplicate save or v1 downgrade.

Capture the current grant under the claim row lock before increment/COMMIT.
Exhausted due rows with no live claim park without returning a capability,
including expired claims left by crashes. Final confirmation or supersession
retains precedence; only unconfirmed final settlement parks. Preserve protected
callback data and due time, clear claims, and retain existing hash, receipt,
transport and token/expiry fences. Direct claim raises typed known parking
metadata after commit. Iteration/delivery return parked status. Passes count
parking and continue admission. Health includes parked work as outstanding while
excluding it from automatic eligibility. Newer staging supersedes parked rows
and clears their protected data.

Package the exact shared Go budget upgrade and optional resume-ledger SQL in
Python generation, wheel and sdist. Add precise single-key selection and guarded
resume using existing command/invocation locks, ordered digest framing and
historical JSON snapshots. Require exact timestamp/count/current-grant fences,
parked v2, no claim, no newer invocation generation/attempt and a larger total
above both old grant and consumed claims. Preserve initial binding, counts and
due time. Atomically retain resumed/conflict/not-found outcomes. Exact retries
read historical evidence first and finish without COMMIT; changed requests under
a retained command ID conflict without overwriting it. V1 adoption stays v1-only.

Reuse the adoption command transaction as a private generic protocol. Public
operations retain owned/shielded cancellation, close cursors before COMMIT and
join rollback/connection cleanup. Do not checkpoint cancellation after confirmed
COMMIT/ROLLBACK. Unknown COMMIT returns no receipt/capability, closes the connection
and remains primary through cleanup. Native integers preserve full int64 generations;
RFC3339 normalization preserves exact microseconds without floating point.

## Consequences

Current helpers in all three SDKs support budgeted scopes; older hosts must be
excluded. Install the budget upgrade before current Python helpers even for v1/
unbound work, and install the separate ledger before resume. Fresh DDL already
includes budget columns and constraints. No existing row is budget-enabled by
DDL. Applications own authorization, durable command persistence, scheduling,
retention and bounded driver/cleanup timeouts. Existing due/stalled watches do
not alert on parked work; monitor its count separately. No automatic negative
disposition, count reset, callback, job acknowledgment or pruning is introduced.
See the [shared budget and rollout contract](../event-acknowledgment-retry-budgets.md#python-budgets-and-guarded-resume).

## Validation

Python 3.11 AST syntax and Ruff passed. Isolated regeneration excluding tests
produced 6525 byte-identical Python/schema/type-marker files; root imports match
canonical generation. SQLC statically compiled 21 unique actual SQL statements
against fresh, upgraded-v1 and pre-policy upgrade paths. Static inspection matched
placeholder/parameter counts at all 28 call sites, all six staging paths, three-SDK
schema bytes and resume request order/domain, Node health query classification,
and exact DDL source binding. Wheel/sdist schema and helper/export bytes match
sources. Shared schema parity, ADR uniqueness, documentation routes/links and
whitespace checks passed. No tests or assertions were added/changed, and no local
tests or SDK helper smoke calls ran. Hosted behavioral qualification remains
pending for mixed SDK recovery, final claims, crashes/unknown commits, concurrent
claim/resume, historical replay/rejections, cancellation, cleanup and supersession.
