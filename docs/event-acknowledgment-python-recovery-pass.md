# Bounded Python callback recovery

`arecover_event_acknowledgment_intents` runs one finite recovery pass for an
account/app's retained callback intents. It composes the existing
[single-intent recovery primitive](event-acknowledgment-python-intents.md#recover-one-saved-callback)
with bounded concurrent admissions and a fresh exclusive connection scope per
admission. The host decides when to call it again; no polling host, idle wait,
background service, schema or route is installed.

Node provides the [same bounded-pass model](event-acknowledgment-node-recovery-pass.md)
with its existing pool ownership, generation limit and AbortSignal contracts.
Go provides the [same finite-pass model](event-acknowledgment-go-recovery-pass.md)
using its existing `database/sql` recovery primitive and context cancellation.

```python
from faas_sdk.preview import (
    EventAcknowledgmentIntentRecoveryPassOptions,
    arecover_event_acknowledgment_intents,
)

result = await arecover_event_acknowledgment_intents(
    client.inner, connection_scope, account_id, app_id,
    EventAcknowledgmentIntentRecoveryPassOptions(max_attempts=100, concurrency=4),
    stop_event=shutdown,
)
```

Use a long-lived authenticated SDK client and an available application recovery
host. The function sends saved callbacks without running business handlers,
preparation or platform renewal. It neither acknowledges ingestion jobs nor
chooses retry/dead-letter decisions. Existing persisted action/input/capability
hashes, database claim tokens/expiry, platform attempt fences and conditional
settlement remain authoritative.

## Admission and budget

Frozen `EventAcknowledgmentIntentRecoveryPassOptions` has these fields:

| Field | Default | Bounds/meaning |
| --- | --- | --- |
| `max_attempts` | 100 | Native integer 1..10000; maximum admitted recovery iterations. |
| `concurrency` | 4 | Native integer 1..32; at most this many admitted iterations/scopes run at once. |
| `claim_duration_ms` | 30000 | Existing database recovery claim duration, 1000..300000 whole milliseconds. |
| `backoff_ms` | None, selecting 1000 ms without a policy | Fixed persisted delay after unconfirmed settlement, 1..3600000 native integer milliseconds. Leave None with a policy. |
| `callback_options` | `None` | Optional frozen `EventAcknowledgmentWorkerOptions`, copied before I/O. |
| `retry_policy` | `None` | Optional frozen `EventAcknowledgmentIntentRetryPolicy` for capped exponential full jitter using durable recovery claims. |

See the [retry policy contract](event-acknowledgment-python-retry-policy.md) for
bounds, count semantics and worker coordination. Policy values are copied before
I/O. Combining a policy and explicit `backoff_ms` is invalid before scope acquisition.

Bound intents use their captured stored policy instead of this worker fallback.
Current intent recovery requires upgraded columns even for unbound rows; see
[binding precedence and rollout](event-acknowledgment-stored-retry-policy.md).

An admission reserves one budget slot before invoking the factory. Successful
settlements, empty probes, acquisition failures and interrupted attempts all
consume slots. The function creates at most `min(concurrency, max_attempts)`
worker tasks, each running one iteration at a time. It never creates a task per
row or keeps a list of job results. Reservations and counters are serialized on
one event-loop thread without awaits between the admission check and reservation.

This budget counts recovery iterations, not HTTP requests, distinct rows or
successful claims. The existing callback budget can retry within an iteration.
An unconfirmed intent can become due again during the same pass; no per-pass ID
tracking, deduplication, fairness or unique-delivery guarantee is added. Global
option ranges are validated before scope acquisition, and the existing primitive
rechecks callback budgets against each saved row's actual lease before committing
its claim. A row-incompatible override fails the pass without claiming that row.
Leaving callback options unset preserves lease-dependent defaults.

New admissions stop when the budget is used, the first empty probe is observed,
the owner stops, or a hard failure/uncertain commit occurs. Unconfirmed callbacks
and lost claims remain per-iteration observations and allow further admissions.
Empty/hard-failure detection stops admission before its scope cleanup finishes.
Already admitted attempts drain normally after an empty probe or hard failure;
owner cancellation signals cooperative stop to all of them.

An empty probe means no due unclaimed row was visible to that iteration's
`SKIP LOCKED` query. Other attempts/hosts may hold claims, rows may become due
later, and concurrent transactions may change the view. Other already-admitted
attempts can still claim/settle after this probe. Neither `empty` nor `limit`
establishes a globally drained backlog. Use independent retained
[intent health](event-acknowledgment-intent-health.md#python-summary) and listing.

## Connection scopes and cancellation

`connection_scope` is a synchronous zero-argument factory returning a fresh async
context manager. Each admission leases an exclusive idle autocommit async psycopg
connection until recovery and context exit finish. Sequential scopes may reuse a
healthy pooled connection; overlapping scopes must never share one connection.
The factory owns pool admission/release and discards connections closed after
unknown COMMIT or failed cleanup. No pass-wide transaction or connection is held.

The claim, preflight and settlement use the existing short transactions. A
connection scope stays leased during its bounded HTTP callback. Size the pool for
the chosen concurrency plus other application work, and bound acquisition,
driver, statement, cleanup and close calls. Factories/context managers must provide
cooperative cleanup and must not wrap recovery in another transaction.

The pass uses the existing shielded ownership boundary. Native task cancellation
or `stop_event` signals cooperative control; active SQL, callback cancellation and
scope cleanup are joined before return or raise. The pass never force-cancels a
worker or abandons an acquired connection. Time bounds depend on the host's driver
and cleanup contracts: a finite work budget is not a hard wall-clock timeout.
Drain the pass before closing clients, pools or its event loop.

A context manager cannot suppress an operation failure or replace uncertain
COMMIT with a different error. Known committed settlement observations survive
later exit failure/cancellation. Failed exit attempts to close the exclusively
leased connection before completion. Acquisition failures expose a fixed pass
failure without raw factory diagnostics; only an actual recovery probe can count
as `empty`.

## Partial progress

`EventAcknowledgmentIntentRecoveryPassResult` is frozen aggregate metadata. It
retains no invocation/consumer IDs, scope values, tokens, bodies, saved results,
reason strings, per-job statuses, connections or raw errors.

| Field | Meaning |
| --- | --- |
| `stop_reason` | `limit`, `empty`, `interrupted`, `failed` or `commit_unknown`. |
| `attempts` | Total admitted iterations, including acquisition failures/empty probes. |
| `confirmed` | Returned locally committed confirmed settlements. |
| `unconfirmed` | Returned locally committed unconfirmed settlements, with persisted backoff. |
| `parked` | Known parking mutations, including exhausted crashed claims without callback delivery. |
| `superseded` | Returned locally committed superseded settlements. |
| `empty` | Actual recovery probes reporting no due unclaimed row. |
| `claim_lost` | Recovery claims expired/replaced before a valid settlement was obtained. |
| `interrupted` | Iterations interrupted without a known returned settlement/probe outcome. |
| `failed` | Other iteration failures without a known returned outcome. |
| `commit_unknown` | Iterations reporting uncertain local COMMIT, including typed uncertainty during scope cleanup. |
| `cleanup_failures` | Scope exit failures/replacement errors observed after an operation outcome/failure. |

These counts describe observations, not unique deliveries. Cleanup/uncertainty
can overlap a known returned outcome: a confirmed settlement followed by scope
failure retains `confirmed` and increments `cleanup_failures`; typed cleanup
uncertainty can additionally increment `commit_unknown`. Do not sum all fields
into a delivery total. Pass interruption can occur during cleanup after a known
outcome, so `stop_reason="interrupted"` can have zero interrupted iterations.
An infrastructure failure before admission can similarly produce a failed pass
with zero attempts. Progress is not a durable audit or a callback-success proof
for any omitted/unsettled iteration.

The function returns normally only for `limit` or `empty`. All admitted work is
joined before these fixed exceptions are raised, each carrying `.result`:

| Exception | Category and precedence |
| --- | --- |
| `EventAcknowledgmentIntentRecoveryPassCommitUnknownError` | Subclass of `EventConsumerCommitUnknownError`; takes precedence over cancellation and other failures. |
| `EventAcknowledgmentIntentRecoveryPassCancelled` | Subclass of `asyncio.CancelledError`; cancellation takes precedence over other ordinary failures, which remain in progress. |
| `EventAcknowledgmentIntentRecoveryPassError` | Other hard failure, including failed scope cleanup after a known settlement. |

Pre-admission cancellation exposes an interrupted zero-attempt result. Invalid
client/factory/scope/options/stop-event input fails before scope acquisition.
Pass failures use fixed messages/codes without forwarding raw causes. Inspect
fresh authoritative retained evidence after unknown COMMIT or claim loss;
neither means the callback failed or that settlement rolled back. Preserve the
original intent and ingestion-job ownership protocols. Do not delete fences,
repeat business work or infer a negative action from a pass exception.

The [host example](../examples/event-acknowledgment-python/README.md#recover-a-bounded-callback-pass)
demonstrates explicit invocation and progress handling. Local validation covers
compilation/lint, Python 3.11 syntax, isolated generation, wheel/sdist inclusion,
shared callback/settlement and existing-example source comparisons, and schema/ADR/
documentation checks. No tests/assertions are added or changed and no local tests
are run. Hosted concurrency/admission, connection isolation, empty races, callback
retry, cancellation, cleanup replacement/suppression and uncertain-commit
qualification remains pending before release.

Budgeted recovery adds a scalar `parked` count and continues admissions after known
parking. Already parked rows are excluded from automatic claims, so empty/limit
can coexist with work awaiting an operator. Apply the budget upgrade before
current helpers, even for v1-only use. See
[parking, exact fences and guarded resume](event-acknowledgment-retry-budgets.md#python-budgets-and-guarded-resume).
