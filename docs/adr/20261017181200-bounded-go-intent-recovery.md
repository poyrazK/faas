# ADR: Bounded Go acknowledgment intent recovery passes

- Status: Accepted
- Date: 2026-10-17

## Context

Go exposes a single saved-callback recovery iteration. Node and Python now offer
finite concurrent passes with explicit progress and joined shutdown. Go needs
the same admission model while preserving its `database/sql`, context and
status-plus-error contracts.

## Decision

Add `RecoverEventAcknowledgmentIntents`, value options/result structs and a fixed
ordinary-failure sentinel. Bound admitted iterations to 1..10000 and concurrency
to 1..32. Reserve each iteration under a mutex before recovery; allocate only
the bounded worker count and retain scalar progress. Join every worker before
returning. Use the supplied context for cooperative database/HTTP cancellation.

Reuse `RecoverNextEventAcknowledgmentIntent` unchanged, preserving saved hashes,
claim SQL/expiry, full int64 generations, callback budgets/retries, platform
fencing and conditional settlement. The first recorded empty, interruption,
hard failure or uncertain commit closes admission. Already admitted work drains.
Unconfirmed settlements and lost claims permit further admissions.

Count a returned committed settlement before interpreting callback-only errors.
In particular, committed unconfirmed/superseded outcomes do not become hard pass
failures. Unknown commit takes precedence, followed by caller interruption,
hard failure, empty and limit. Return progress alongside joined fixed sentinels
recognized by `errors.Is`; retain no raw errors or caller cancellation causes.

Copy options before I/O, validate broad bounds without replacing zero callback
defaults, and preserve the existing per-row validation in `Deliver`. Incompatible
overrides can therefore leave an acquired claim to expire. Standard `sql.DB`
owns connections; the pass adds no transaction or callback-long connection scope.

## Consequences

Hosts own future scheduling, pool/client lifetime and bounded driver/transport
operations. Empty remains a local due/unclaimed observation, without global
drain, uniqueness, fairness, ordering or deduplication guarantees. There is no
new schema, route, registry, migration, poller, pruning, business handler,
platform renewal, ingestion-job ack or automatic negative decision. See the
[pass contract](../event-acknowledgment-go-recovery-pass.md) and
[buildable host example](../../sdk/go/examples/event-acknowledgment-recovery/README.md).

## Validation

Use standalone SDK/example builds and vet, static Go AST admission/result/error
and unchanged primitive/schema comparisons, plus schema/ADR/documentation checks.
Add no tests, change no assertions and run no local tests. Hosted concurrency,
empty races, retries, connection isolation, cancellation and uncertain-COMMIT
qualification remains pending before release.
