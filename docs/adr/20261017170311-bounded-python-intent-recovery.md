# ADR: Bounded Python acknowledgment intent recovery passes

- Status: Accepted
- Date: 2026-10-17

## Context

Python exposes one fenced saved-callback recovery iteration. Applications must
otherwise build finite catch-up batches, connection ownership, concurrency control
and joined shutdown themselves. These responsibilities should compose the existing
intent protocol without adding business execution or a scheduler.

## Decision

Add `arecover_event_acknowledgment_intents` with frozen pass options and aggregate
results. Bound admissions to 1..10000 and concurrency to 1..32. Reserve each slot
before scope acquisition, create at most the bounded worker count and retain no
per-job result list. Each admission calls the existing single-intent primitive
inside a fresh exclusive idle autocommit async connection scope. Preserve its
claim, preflight, HTTP budget/backoff and conditional settlement behavior.

Stop new admissions on budget, empty probe, owner stop, hard failure or uncertain
commit. Allow unconfirmed callbacks/lost claims to remain independent iteration
outcomes. Drain admitted work after empty/hard failures; owner cancellation signals
cooperative stop while shielding and joining SQL, HTTP cancellation and scope
cleanup. No worker is force-cancelled and no idle polling or scheduling is added.

Preserve known returned settlement/probe observations across exit failure. Scope
suppression/replacement cannot erase operation failures or unknown COMMIT. Attempt
connection close on failed scope exit. Keep cleanup/uncertainty counts orthogonal
to known outcomes where necessary. Expose frozen partial progress with fixed
failure, native cancellation and uncertain-COMMIT exception classes; uncertainty
precedes cancellation, then ordinary failure, then empty/budget completion.

Use existing claim/backoff bounds and snapshot callback overrides before I/O.
Validate broad callback ranges at entry and let the existing primitive validate
each row's actual lease before claim COMMIT. Hosts supply bounded connection/
cleanup calls, available clients/loops, pool sizing and explicit future scheduling.

## Consequences

No SQL, schema, route, migration, compliance registry, existing processing behavior
or recovery fence changes. No business handler, preparation, platform renewal,
ingestion-job acknowledgment, automatic negative decision, retention or pruning is
introduced. Empty is a local due/unclaimed observation, without global-drain,
unique-row, fairness, ordering or deduplication guarantees. Aggregate progress
retains no job IDs, payloads, tokens, saved results or raw diagnostics. See the
[pass contract](../event-acknowledgment-python-recovery-pass.md).

## Validation

Use compilation/lint, Python 3.11 syntax, isolated generation, wheel/sdist inclusion,
static ownership/admission/result and unchanged primitive/SQL/example comparisons,
plus schema/ADR/documentation checks. Add no tests, change no assertions and run
no local tests. Hosted concurrency, cancellation, scope cleanup, independent retry,
empty races and uncertain-commit qualification remains pending before release.
