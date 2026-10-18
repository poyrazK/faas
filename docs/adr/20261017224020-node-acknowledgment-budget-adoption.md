# ADR: Node guarded budget-adoption parity

- Status: Accepted
- Date: 2026-10-17

## Context

Go can attach finite v2 budgets to existing unbound/v1 callback backlog, preserving
protected inputs, consumed claims and due times. Node needs the same operator
command, exact timestamp fences and retained receipts within its safe integer
range, without altering existing v1 adoption or resume behavior.

## Decision

Add a separate Node budget-adoption command and precise candidate read, reusing
the existing dedicated command transaction and exact metadata parser. Require
complete own-data options, a positive int32 total above consumed claims, and an
explicit unbound/v1 expectation. Omitted/null expected policy means unbound;
v1 metadata requires complete timing and zero/omitted initial budget. Preserve
v1 timing after normalization. Copy/freeze all policy/budget values before I/O.
Reject unsupported mutation generations before numeric conversion or storage;
diagnostics retain full int64 text. Canonicalize RFC3339 microseconds without
rounding through Date.

Use Go's exact optional ledger SQL, command/invocation lock namespaces, fourteen
ordered digest fields and historical snake-case snapshot. Apply identical SQL
guards for timestamp/count/policy, active unbound/v1 state, no live claim, no newer
invocation generation/attempt and a total above consumed claims. Change only five
policy/grant columns and UpdatedAt. Preserve callback/state/count/due time and
expired claim metadata. Successful snapshots may have zero consumed claims and
an expired claim; they use a separate validator from resume.

Retain adopted/conflict/not-found outcomes atomically. Matching commands replay
historical evidence before current rows, without another COMMIT. Changed valid
input under a retained UUID conflicts without a receipt/overwrite. New commands
and replay validate frozen metadata against the original normalized request.
Unknown COMMIT returns no receipt, retains the existing uncertainty category and
discards the connection. Reuse joined cleanup/error precedence; driver exceptions
are sanitized and secondary release failure cannot erase primary uncertainty.
V1 adoption/resume retain their independent ledgers and implementations.

## Consequences

Applications explicitly install the shared budget-adoption ledger beside the
budget-upgraded intent table. Existing helpers do not acquire a new schema
dependency. Go and Node can resolve each other's original commands within Node's
safe generation range; Python command parity follows. Current budget-aware
recovery in any SDK can consume the resulting binding within its supported range.
Applications own authorization, durable command persistence, receipt retention,
scheduling and bounded acquisition/query/cleanup lifetimes. No callback, business
work, platform renewal, count reset, ingestion-job acknowledgment or pruning is
introduced. See the [Node command and replay contract](../event-acknowledgment-budget-adoption.md#node-selection-adoption-and-replay).

## Validation

Node generation produced no generated API drift and the TypeScript build passed.
SQLC statically compiled 25 unique combined statements against fresh, upgraded-v1
and pre-policy upgrade paths; all seven new query call sites have matching
positional argument counts. Static inspection matched Go's request field order,
digest domain, lock namespace, mutation guards and six-column write set, plus
exact source DDL in generation/static compilation. The Node archive contains
byte-identical helper JS/declarations, ledger contract, root exports and README.
Shared schema parity, example syntax, generator Ruff, ADR uniqueness,
documentation routes/links and whitespace checks passed. No tests/assertions were
added or changed, and no local tests or SDK helper smoke calls ran. Hosted
behavioral qualification remains pending for Go/Node receipt replay, unbound/v1
adoption, claim/staging races, expired/live claims, cancellation, unknown commit,
cleanup failure and supersession.
