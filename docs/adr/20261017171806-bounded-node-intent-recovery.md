# ADR: Bounded Node acknowledgment intent recovery passes

- Status: Accepted
- Date: 2026-10-17

## Context

Node exposes one saved-callback recovery iteration. Python now offers finite
concurrent passes with explicit partial progress and joined shutdown. Node needs
the same admission model using its existing pool/transaction ownership contract.

## Decision

Add `recoverEventAcknowledgmentIntents`, pass options/result interfaces and fixed
ordinary-failure, AbortError and uncertain-COMMIT exception classes. Bound admitted
iterations to 1..10000 and concurrency to 1..32. Reserve before recovery without
an intervening await, create only the bounded worker count and retain fixed scalar
progress rather than per-job statuses. Await all workers before return or raise.

Reuse `recoverNextEventAcknowledgmentIntent` for every admission, preserving all
SQL, input/capability validation, database claim/expiry, HTTP budgets/backoff,
platform fencing and conditional settlement. Empty rejection, owner abort, hard
failure or uncertainty closes admission. Unconfirmed settlements/lost claims can
continue independently. Already admitted attempts drain after empty/hard failures.

Delegate each short transaction through a per-admission pool guard. Preserve query
bytes/parameters, receiver binding and discard flags. Record synchronous release
exceptions without letting them replace the operation's outcome or unknown COMMIT;
close admission and abort later work in the affected iteration. Preserve known
returned settlements and orthogonal cleanup/uncertainty counts. Never retry an
ambiguous release; pool validity and synchronous release remain host contracts.

Snapshot closed own-data options and callback overrides before I/O, and preserve
per-row callback budget validation. Use fresh internal abort signals with fixed
reasons. Uncertainty precedes native AbortError, then ordinary failure, then
empty/budget completion. Expose frozen aggregate progress without IDs, tokens,
payloads, statuses, raw causes or caller abort reasons. Preserve the recognizable
unsupported-generation code when Node's existing safe-integer limit causes the
ordinary failure category; never round or silently skip that row.

## Consequences

Hosts own explicit invocation, future scheduling, pool/client/loop lifetime and
bounded driver calls. No poller, idle wait, service, schema, route, migration,
registry, pruning, business handler, platform renewal, ingestion-job ack or
automatic negative decision is introduced. Empty remains a local due/unclaimed
observation, without global-drain, uniqueness, fairness, ordering or deduplication
guarantees. See the [pass contract](../event-acknowledgment-node-recovery-pass.md).

## Validation

Use generation, TypeScript build/declarations, package inclusion, example syntax,
static admission/result/error and unchanged primitive/SQL/example comparisons,
plus schema/ADR/documentation checks. Add no tests, change no assertions and run
no local tests. Hosted concurrency, empty races, retries, pool isolation/release,
abort, generation boundaries and uncertain-COMMIT qualification remains pending
before release.
