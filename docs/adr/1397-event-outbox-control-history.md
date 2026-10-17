# ADR-1397 · Durable receipts for application outbox recovery controls

Date: 2026-10-15
Status: Accepted

## Context

Outbox reconciliation and guarded resume currently retain only their latest local
state. Operators need retained outcomes and a way to recover a control result
after a lost response without reapplying it or confusing a new observation with
the original command.

## Decision

Opt into durable history through a nonzero UUID CommandID on the existing Go SDK
control options. Preserve legacy calls without an ID. Scope command identity to
account/application, bind immutable action parameters by digest, and serialize
identical commands with a transaction-scoped advisory lock.

Execute the local control and insert its typed, payload-free result in the same
transaction. Record safe negative observations/rejections as well as successes;
never retain raw remote or driver diagnostics. Retained replay returns the original
result and stable error without a new verification or state update.

Add application-owned receipt and per-key transactional sequence-counter tables.
Allocate sequences at history insertion, holding the counter lock through commit,
so later visible receipts cannot precede earlier commits during pagination. Keep
counters across receipt pruning and history independent of publication-row pruning.
Local parameterized SQL follows the existing static SQLC validation exception for
application outbox helpers. No platform endpoint or database migration is added.

## Consequences

Command-bearing controls fail closed when history storage is unavailable. They
hold a database transaction and per-command advisory lock during bounded remote
verification, but no publication row or history counter lock across HTTP. Callers
must bound complete operations and retain command IDs/results for their replay
horizon. History is metadata, not authenticated actor attribution or a complete
log of legacy/direct-SQL interventions. Retention remains application-owned and
does not extend publication deduplication, retry windows, or ordering guarantees.

See [installation, replay and retention](../event-outbox-control-history.md).
