# ADR-1579: Internal CI health and bounded worker observations

- Status: implemented behind existing default-off internal gates
- Date: 2026-10-16
- Extends: ADR-1337, ADR-1339, ADR-1341, ADR-1342, ADR-1577 and ADR-1578

## Decision

Add `GET /v1/internal/ci/health` and `gregalectl ci health` for account-scoped
queue and quota diagnostics. Apply the existing internal account allowlist,
active-account authentication, admin scope, MFA, trusted HTTPS origin and
no-store response boundary. Reads work with compute, storage, automation and
Check publication disabled. They never call GitHub, GCS, the scheduler or vmmd.

Use a read-only repeatable-read PostgreSQL transaction with a five-second
context bound. Queues, quotas, worker observations and observation time share
one database snapshot. Read errors fail the request; unavailable data never
becomes healthy zero counts. No execution snapshot or authenticated source
snapshot is decoded. The response contains no account, repository, commit,
execution, host, object, capability or private request identity.

Report the configured apid gates separately from durable observations. A
configured worker is not proof of progress, transport availability, scheduler
capacity or native compute qualification. Public compute capabilities remain
false; native KVM/GCS/GitHub acceptance remains a separate release gate.

## Queues and quotas

Report execution reconciliation, metadata admission, automatic admission,
metadata Checks, execution Checks, unapplied Check retry requests and storage
retirement. Include pending, in-flight, dead, time-due and expired-lease counts.
Execution Checks include acknowledged versions which have become dirty again;
archived executions leave live queue accounting. Source-event usage combines
both source journals, including their retained deduplication evidence.

Conditions overlap: an expired in-flight lease can also be due. Due means time
eligible, without claiming authority, a free attempt budget, native safety or
scheduler capacity. Oldest-item age is measured from the creation of the oldest
retained pending/in-flight item, not from its last state transition. A Check can
become dirty after a long successful period, so this age is deliberately not a
delivery latency SLO. Retirement counts expired/orphan/deleting candidates,
including candidates whose existing native safety checks still prevent cleanup.

Account usage includes detailed executions, active executions, dispatcher
leases, archives, profiles, revisions, metadata receipts, source events, live
storage bytes/objects, permanent object journal rows and active storage I/O.
Limits come only from `pkg/api/limits.go`; remaining quota is clamped at zero.
Archived detail slots become free while archive and tombstone budgets remain
charged. There is no fleet equivalent of a per-account history limit.

## Bounded worker evidence

Use six fixed worker kinds: dispatcher, metadata admission, automatic admission,
metadata Checks, execution Checks and retention. Each has eight diagnostic slots,
for at most 48 fleet rows. Slots contain random incarnation tokens but expose
only counts by condition and latest heartbeat/cycle timestamps. No worker token,
slot number or machine identity is returned or used as a metric label.

Each worker group pulses independently every ten seconds with a five-second
write bound. Reconciliation completion, including an empty queue, advances an
in-memory sequence, including each bounded per-account decision in a longer
automatic-admission sweep; the next pulse records progress and the latest outcome using
the database clock. The dispatcher observation describes its four local loops
as one group, not four replicas. Multiple completions between pulses coalesce;
this is sampled operational evidence, not a per-attempt audit or failure counter.
A successful empty poll can clear a previous failed-cycle condition.

Configured-off workers pulse as disabled; enabled workers without their wired
dependencies pulse as blocked. A fresh group without a completed cycle is
starting; the latest failed cycle is degraded; a successful cycle is healthy.
No progress for 120 seconds is stalled, even when heartbeats continue. No pulse
for 45 seconds is stale. A role status chooses an available healthy group first;
condition counts still show partial failures. Unknown means no retained evidence,
not disabled. Disabled processes cannot overwrite another replica's enabled slot.

At 90 seconds without a pulse, a slot can be reclaimed with a new token, clearing
its old progress. An expired or replaced incarnation cannot resume that slot.
Claim retries recover the same token/slot after a lost acknowledgement. A durable
cycle sequence makes repeated pulse samples idempotent: they refresh presence
without refreshing progress or erasing the last outcome. Acquiring a new slot
baselines the current sequence, so old in-memory completions are not new progress.
Old crashed observations remain stale until reuse. Eight fresh groups exhaust
diagnostic capacity for a kind; later groups continue reconciling without a slot.
Thus slot counts are bounded sampled observations, not a complete replica census.

Health writes, slot exhaustion and observation failures cannot pause, grant,
cancel or fence execution work. Worker leases remain the existing queue leases.
Enabled workers which exit stop pulsing rather than being reported disabled.
Cancellation joins monitoring goroutines and all apid CI workers before closing
their providers or database. No errors, connection details or signed URLs enter
the health journal; the only failure classification is `unavailable`.

## Metrics and dormant defaults

Register on the existing private apid and githubd `wire.OpsMetrics` registries.
Expose `*_ci_health_enabled`, `*_ci_health_up`, queue counts/oldest item ages,
resource usage/global limits/headroom, and worker condition counts. Labels are
fixed queue, condition, resource and worker enums. There are no account,
repository, branch, commit, execution, host or error-content labels. Fleet
aggregates are private metrics; account API reads never use the fleet selector.

When every CI flag is off, no health reads, pulses or CI workers run; only the
configured-zero metric is emitted. Internal access or an independently enabled
worker/publication/retention path enables observation. This also permits evidence
while an execution or retention worker drains with admission off. A failed scrape
emits health-up zero and omits dependent evidence. Gate alerts on observation
being enabled; missing/unseen worker evidence requires investigation, not a
healthy interpretation. Fleet aggregates repeat on each daemon: do not sum them
across replicas or both daemons.

Add one append-only migration. Rollback drops only diagnostic observations;
ongoing worker health writes can fail harmlessly and health reads report errors.
Restart/recovery never enables a flag or backfills execution/Check intent.
All shipped flags and account allowlists remain unchanged and off/empty.

## Validation

PostgreSQL tests cover account isolation, qualification exclusion, stale queue
leases, dirty Check versions, dead delivery/retry recovery, archived quota reuse,
worker failure/stall/crash/restart fencing and slot/schema bounds. Portable
worker lifecycle tests cover disabled work and diagnostic database failures.
Prometheus tests check bounded label names, dormant scrapes, duplicate
registration and failed-read omission. API/CLI and apid tests cover internal
routing, HTTPS, scope and no-store behavior. No native VM or real provider
qualification is implied by these checks.
