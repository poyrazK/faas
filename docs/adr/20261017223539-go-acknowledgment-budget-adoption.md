# ADR: Guarded Go budget adoption for existing callback backlog

- Status: Accepted
- Date: 2026-10-17

## Context

All three SDKs can stage and recover v2 budgets, but existing unbound/v1 intents
remain unlimited. Operators need to attach a finite total without changing
callback decisions, resetting consumed claims or losing the ability to resolve
an uncertain local commit.

## Decision

Add a Go budget-adoption command for selected active unbound/v1 rows. Require
exact timestamp/count/policy expectations, no live claim and no newer generation/
attempt. Nil expected policy means unbound; a v1 expectation must be complete and
retain its timing. Normalize and copy input before I/O. Require a positive int32
total strictly above consumed claims. Set the immutable initial budget and current
grant together, preserving callback data, action/reason, state, counts, due time
and expired claim metadata. Strictly advance UpdatedAt by at least a microsecond.
The command never parks a row immediately or acquires a recovery capability.

Use a separate optional application-owned receipt ledger, with the same bounded
metadata/outcome shape as v1 adoption but its own table, command lock namespace,
action and ordered digest framing. Serialize command UUID, then invocation, then
row, coordinating with staging/recovery and other commands. Atomically retain
adopted/conflict/not-found outcomes. Exact original commands replay historical
results before checking current rows, without another COMMIT, even after recovery,
resume, supersession or intent pruning. Different valid inputs under a retained
UUID conflict without overwriting it. Unknown COMMIT returns no receipt and must
be resolved using the original command; driver diagnostics remain sanitized.

The v1 adoption command and resume contract stay independent. A receipt's v2
snapshot has equal initial/current grants and fewer consumed claims, validated
against the original request on both new commands and replay. Current Go/Node/
Python recovery can consume the resulting binding within each SDK's generation
range; command API parity in Node/Python follows separately.

## Consequences

Applications explicitly install the new receipt schema beside the budget-upgraded
intent table. Other intent helpers do not require the ledger. No platform migration,
existing-column upgrade, automatic budget backfill, callback, business work, job
acknowledgment, count reset or pruning is introduced. Retain command receipts for
the full retry horizon independently of intents. Applications own authorization,
durable command persistence, host compatibility, scheduling and bounded SQL/context
timeouts. An expired in-flight remote callback may still complete; adoption keeps
its original inputs and does not establish remote failure. See the
[adoption and receipt contract](../event-acknowledgment-budget-adoption.md).

## Validation

Standalone Go SDK/example build and vet passed. SQLC statically compiled all 25
unique combined storage statements against fresh, upgraded-v1 and pre-policy
upgrade schemas, with positional argument counts checked at all 34 call sites.
Static inspection confirmed the new ledger preserves the existing receipt DDL
shape, all 14 request fields match documented framing, every concurrency guard
is present, and the update changes only five policy/grant columns plus UpdatedAt.
Current SQLC inputs contain the exact source DDL. Shared schema parity, gofmt,
ADR uniqueness, documentation routes/links and whitespace checks passed. No tests
or assertions were added/changed and no local tests or SDK helper smoke checks
ran. Hosted behavioral qualification remains pending for unbound/v1 adoption,
claim/staging races, newer attempts, expired/live claims, historical replay,
resume interoperability, cancellation, unknown commit and independent outcomes.
