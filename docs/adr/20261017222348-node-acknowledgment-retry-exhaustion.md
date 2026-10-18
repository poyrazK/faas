# ADR: Node acknowledgment retry-budget parity

- Status: Accepted
- Date: 2026-10-17

## Context

Go's application-owned v2 intent contract bounds committed recovery claims,
retains exhausted callbacks as parked work, and grants further claims through
an explicit idempotent command. Node must participate without ignoring those
limits, rounding operator fences or changing receipt and callback ownership.

## Decision

Add a separate optional Node `retryBudget` staging option with required positive
int32 `maxDeliveryAttempts` and optional timing `retryPolicy`. Copy and freeze the
binding before asynchronous work, reject ambiguous top-level policy/budget options,
and thread it through all six staging paths, including prepared receipt replay.
Keep omitted staging unbound and v1 timing/fallback policies unlimited. Decode
v2 initial limits and current grants strictly; repeat staging compares the initial
binding, allowing idempotency after resume without resetting the grant/count.

Capture the current grant under the claim lock before increment/COMMIT. Exhausted
due rows with no live claim park without returning a capability. The final allowed
claim still confirms or supersedes normally; unconfirmed settlement parks. Retain
protected callback data and due time, clear the local claim, and preserve original
hash validation, receipt preflight, transport limits and token/expiry fences.
Direct claim returns typed known parking metadata only after commit. Iteration
and delivery return parked status in Node's existing status-based outcome model.
Passes count parking and continue admission; health includes parked work as
outstanding while excluding automatic eligibility metrics. Newer staging also
supersedes parked predecessors and clears secrets.

Package Go's exact budget-upgrade and optional resume-ledger SQL in the Node
schema contract generator. Add a precise selection API and guarded resume with
Go's command key, advisory locks, ordered digest framing and historical JSON
snapshot. Require exact microsecond timestamp/count/grant fences, parked v2,
no claim, no newer generation/attempt and a total strictly above the old grant
and consumed count. Retain resumed/conflict/not-found receipts atomically. Read
exact retries before current rows, without another COMMIT. Reject changed input
under a retained UUID. Keep diagnostic int64 text but reject unsupported mutation
generations before I/O. V1 adoption remains v1-only.

Reuse the adoption command transaction's dedicated-connection/read-replay protocol,
with joined cleanup and sanitized synchronous/asynchronous driver errors. Commit
uncertainty returns no receipt/capability and cannot be replaced by a secondary
release failure. The ordinary intent transaction now preserves that same primary
error precedence. Recovery-pass pool guards continue to record cleanup failures
alongside known observations.

## Consequences

Install the budget upgrade before current Go/Node helpers, even for v1/unbound
work; fresh installations already include its columns/constraints. Install the
resume ledger separately before commands. No existing work is budget-enabled by
DDL. Budgeted scopes require current Go/Node hosts; Python runtime support is
pending. Existing due/stalled watches do not alert on parked work, which needs
separate application monitoring and explicit operator action. No automatic
negative disposition, count reset, callback, job acknowledgment or pruning is
introduced. Applications own authorization, command persistence, retention,
scheduling and bounded driver/cleanup lifetimes. See the
[budget and rollout contract](../event-acknowledgment-retry-budgets.md#node-budgets-and-guarded-resume).

## Validation

Node generation has no generated API drift and the full TypeScript build passed.
SQLC statically compiled all 22 actual storage queries against fresh, upgraded-v1
and pre-policy upgrade paths. Shared schema generation parity and static Go/Node
request field-order/domain checks passed, covering all six staging paths and exact
DDL source binding. The Node archive contains byte-identical built JS/declarations
and the new public exports. Example syntax, generator Ruff, ADR uniqueness,
documentation routes/links and whitespace checks passed. No tests/assertions were
added or modified, and no local tests or SDK helper smoke checks were run. Hosted
behavioral qualification remains pending for mixed Go/Node scopes, concurrent
claim/resume, final claims, crashes/unknown commits, replay/rejections, cancellation,
cleanup failure and supersession.
