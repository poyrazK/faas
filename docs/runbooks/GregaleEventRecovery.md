# Acknowledgment callback recovery health

Source: [recovery rules](../../examples/event-acknowledgment-monitoring/recovery.rules.yml)
and [setup guide](../../examples/event-acknowledgment-monitoring/README.md#recovery-health-node).
All alerts use `family=event_recovery`, `severity=warn`. Six observe retained
application-owned callback intents; three opt-in worker alerts observe actual
local pass activity. None starts recovery or changes a budget.

## Establish scope and evidence

Select the alert's `job`/`instance` in dashboard `gregale-event-recovery`. Resolve
its owner scope from trusted host configuration under the application's diagnostic
authorization. An all-target absence alert has no affected instance; inspect
expected discovery first. Never put IDs, tokens, callbacks or payloads in metrics,
annotations or diagnostic logs. Each target describes one bounded database scope;
duplicate observers must not be summed into an application/global total.

Check scrape reachability, read success, completeness, observation age/bound and
the eligible-evidence recording. Backlog panels deliberately show Unknown when
proof is unavailable. A reachable scrape alone does not establish live polling.
An empty complete retained sample does not cover never-recorded/pruned work or
prove business processing and callback confirmation.

## Unavailable observations

`GregaleEventRecoveryEvidenceUnavailable` means a reachable target lacks complete,
fresh advancing evidence for two minutes. Inspect pool acquisition/statement/
release timeouts, database access, polling lifetime, event-loop delays and host/
database/Prometheus clocks. Repeated/regressing/future observations cannot confirm
health. Invalid timestamp rows or scan truncation also invalidate eligibility.
Increase the scan limit within 1..10000 only after reviewing query cost; retain
coverage flags with counts. A bounded incomplete sample is a lower bound.

`GregaleEventRecoveryScrapeDown` means a discovered target is unreachable for five
minutes. Check the explicit listener, process, network namespace and scrape
configuration. `GregaleEventRecoveryTargetsMissing` means no opted-in targets
exist for ten minutes. Restore expected discovery; disable that specific absence
alert if intentional zero-target intervals are normal. Removed targets need the
application's inventory policy when other targets remain.

Restore observation access before declaring backlog clear. Prometheus backlog
alerts stop matching when their evidence guard disappears and can send a resolved
notification. This does not resolve callback work. Inspect companion unavailable/
down/absence alerts and authoritative metadata. The
[checkpointed Node](../event-acknowledgment-node-checkpoints.md) and
[Python watches](../event-acknowledgment-python-checkpoints.md) retain incidents
across evidence loss when that notification contract is needed.

## Parked callbacks and guarded resume

`GregaleEventRecoveryParked` means at least one retained parked callback in
eligible observations for five minutes. Parked work exhausted its total committed
recovery-claim grant. It retains protected callback inputs and is excluded from
due/stalled eligibility; a clear automatic backlog does not clear parked work.

1. Authorize the database owner scope and inspect bounded health/list metadata
   through `getEventAcknowledgmentIntentHealth` and `listEventAcknowledgmentIntents`.
   Preserve parked capabilities and immutable decisions; retention selects only
   confirmed/superseded terminal rows.
2. Diagnose dependency failure, unsupported worker rollout or unknown callback
   outcome before approving additional claims. Counts measure committed claims,
   including crashes/uncertain COMMIT; they are not physical HTTP request totals.
3. Select current parked metadata using
   `getEventAcknowledgmentIntentRetryResumeCandidate`. Approve a larger total
   with the exact timestamp/count/current-grant guards. Install the optional
   retry-resume receipt ledger before use.
4. Durably retain the command UUID and all original inputs, then call
   `resumeEventAcknowledgmentIntentRetryBudget` under operator authorization.
   After uncertainty, retry those original inputs on a fresh connection/context;
   do not silently refresh guards or replace command identity.
5. Resume returns work to recovery eligibility; it does not confirm the callback.
   Use the application's bounded recovery host and inspect authoritative results.
   Newer staging can supersede parked work. Either action can lower parked count
   without proving business success.

Never reset consumed claims, delete parked rows, rewrite saved callbacks or grant
claims automatically because an alert fired. See
[budget/resume semantics](../event-acknowledgment-retry-budgets.md#node-budgets-and-guarded-resume)
and the [Node operator example](../../examples/event-acknowledgment-node/retry-budget.mjs).
For unbound/v1 backlog, the separate
[guarded budget-adoption command](../event-acknowledgment-budget-adoption.md#node-selection-adoption-and-replay)
preserves its existing counts and callback; it cannot replace parked resume.

## Due and stalled work

`GregaleEventRecoveryDueBacklog` means at least 100 due callbacks for five minutes.
`GregaleEventRecoveryStalled` means at least one callback eligible beyond the host's
stall interval for five minutes; the example stall interval is five minutes.
Live claims, future retry schedules and parked rows are excluded from these
conditions. Check the recovery host's scheduling, compatible workers, PostgreSQL
reachability and platform callback dependencies, then inspect exact retained work.

Use an explicitly scheduled [bounded Node recovery pass](../event-acknowledgment-node-recovery-pass.md).
Preserve conditional claim/platform fences and uncertain-COMMIT recovery. The
alert authorizes no business replay, negative acknowledgment, ingestion-job
deletion or fence takeover. A recovery pass's empty probe does not prove global
drain. Confirming one callback concerns its original consumer/attempt only.

Thresholds and hold times are starting values, not delivery SLOs. Tune them with
scrape/evaluation intervals and documented coverage. Local static checks do not
qualify live Prometheus notifications, Grafana rendering or callback recovery;
hosted behavioral qualification remains pending.

## Python hosts and guarded recovery

The [Python recovery exporter](../event-acknowledgment-python-recovery-prometheus.md)
uses the same rules, dashboard and alert meanings. Keep its ASGI app, poller and
cache in one loop/process; `read_success=0` or absent backlog is unknown evidence.
If queries hang, restore bounded driver/cleanup behavior rather than starting
overlapping reads. Stop/event/native cancellation joins active work before the
cache is cleared and the pool can close. Treat failed connections as discarded.

Use trusted scope and the SDK-specific APIs below with fresh exclusive idle
autocommit async connection leases. Select immutable metadata under diagnostic
authorization; a read grants no callback claim or mutation authority.

| Operation | Python API |
| --- | --- |
| Read bounded backlog | `aget_event_acknowledgment_intent_health` |
| List retained metadata | `alist_event_acknowledgment_intents` |
| Select precise parked metadata | `aget_event_acknowledgment_intent_retry_resume_candidate` |
| Apply an approved larger total grant | `aresume_event_acknowledgment_intent_retry_budget` |
| Adopt a budget for active unbound/v1 work | `aadopt_event_acknowledgment_intent_retry_budget` |
| Explicit bounded callback recovery | `arecover_event_acknowledgment_intents` |

For parked resume, approve the exact owner/key, native count/current-grant and
canonical microsecond timestamp fences, and a larger total. Install the optional
resume ledger and durably retain the complete original command UUID/inputs before
calling. An uncertain COMMIT provides no receipt: discard that connection and
retry the original command on a fresh connection/context. Do not refresh guards
or replace identity silently. Native full int64 generations stay intact.

A known resume returns eligibility, not callback confirmation; newer staging can
also supersede parked work. Preserve protected callback inputs and counts, and
retain only confirmed/superseded rows as terminal. See
[Python budgets/resume](../event-acknowledgment-retry-budgets.md#python-budgets-and-guarded-resume),
[the operator example](../../examples/event-acknowledgment-python/retry_budget.py)
and the separate [unbound/v1 adoption contract](../event-acknowledgment-budget-adoption.md#python-selection-adoption-and-replay).
Use the [bounded Python recovery pass](../event-acknowledgment-python-recovery-pass.md)
only through the application's scheduled owner policy. Alerts authorize no
business replay, negative callback or ingestion-job deletion.

## Go hosts and guarded recovery

The [Go recovery exporter](../event-acknowledgment-go-recovery-prometheus.md) uses
the same rules, dashboard and evidence meanings. Its cache serializes concurrent
polling/scraping. Inspect the explicit poller lifetime, database driver deadlines
and protected HTTP listener separately; successful scraping does not prove live
polling. Cancellation joins the active read and clears cached evidence before
return. Keep the pool available until polling and HTTP shutdown finish.

| Operation | Go API |
| --- | --- |
| Read bounded backlog | `GetEventAcknowledgmentIntentHealth` |
| List retained metadata and select parked guards | `ListEventAcknowledgmentIntents` |
| Apply an approved larger total grant | `ResumeEventAcknowledgmentIntentRetryBudget` |
| Adopt a budget for active unbound/v1 work | `AdoptEventAcknowledgmentIntentRetryBudget` |
| Explicit bounded callback recovery | `RecoverEventAcknowledgmentIntents` |

Use trusted scope and bounded list results under diagnostic authorization. Select
the exact parked row/key, `UpdatedAt`, `DeliveryAttempts` and current
`DeliveryAttemptLimit`. Approve a larger total and durably save the original
command UUID/all inputs before applying it; install the optional resume ledger.
Preserve the timestamp's database precision and int64 generation/count fences.
After unknown COMMIT, retain the original command, use a fresh connection/context
and retry identical inputs. Do not refresh guards or replace command identity.
See the [buildable resume example](../../sdk/go/examples/event-acknowledgment-recovery/retry_budget.go)
and [budget contract](../event-acknowledgment-retry-budgets.md).

Resume restores recovery eligibility, not callback confirmation. Newer staging
can also supersede parked work; preserve protected callbacks and consumed claims.
Unbound/v1 work uses the separate
[budget-adoption command](../event-acknowledgment-budget-adoption.md) and ledger.
Use the [bounded Go recovery pass](../event-acknowledgment-go-recovery-pass.md)
only under application scheduling/authorization. For retained incident identity
across unavailable evidence/restart, use the
[Go checkpointed watch](../event-acknowledgment-go-checkpoints.md). Alerts grant no
business replay, budget mutation or callback action authority.

## Worker liveness

The [Go actual-pass observer](../event-acknowledgment-go-worker-liveness.md) and
[Node observer](../event-acknowledgment-node-worker-liveness.md) add
worker liveness independently of database health reads. Enable
`gregale_event_recovery_worker="true"` only on a combined observer target and
retain the existing recovery target label. A health-only reader cannot establish
that callbacks are being recovered. Select the same authorized job/instance and
inspect the worker panels separately from backlog/coverage panels.

`GregaleEventRecoveryWorkerOverdue` means the first/next pass failed to start
within its idle expectation, or an active pass has not returned within its
duration expectation, for two minutes. Before the first pass the idle clock runs
from cache construction. Defaults are one-minute idle and five-minute active
duration bounds; these are not force-cancellation deadlines. Inspect scheduler
lifetime, active-pass flag, actual start/return times, pool/driver and callback
deadlines. Scraping/health polling does not refresh activity. Never overlap an
unreturned pass, reset its observation cache to hide it or take over its fences.
Restore bounded operations and join admitted work before reusing resources.

`GregaleEventRecoveryWorkerEvidenceUnavailable` means a reachable opted-in target
lacks valid worker timing/clock/bounds for two minutes. Check the combined handler,
fixed prefixes, target opt-in and host/Prometheus clocks. A detected local clock
regression remains invalid until the clock is reviewed and the host explicitly
replaces the cache. Do not interpret missing/invalid activity as healthy. Existing
scrape-down/target-absence alerts cover correctly configured recovery targets;
removed subsets need an external inventory policy.

`GregaleEventRecoveryWorkerPassFailed` means failed or unknown-COMMIT admissions,
or Node pool-cleanup failures,
remain in the latest valid joined pass for two minutes. It does not fire for
interruption alone. Inspect the original returned progress and joined error,
which metrics do not retain. Unknown COMMIT takes precedence; preserve saved
intents and reconcile through the existing conditional recovery protocol before
choosing another pass. Do not refresh immutable callback decisions or consumed
claim counts. The alert itself grants no retry or business/callback authority.

All pass outcomes are latest-pass gauges, retained while the next pass is active.
They are not cumulative counters, rates or current database backlog. A parked
outcome is not total retained parked work; an empty probe or limit stop does not
prove global drain. Joined return includes failure/cancellation and does not
confirm all callbacks. Malformed progress omits outcome gauges. Keep original
progress/error under the host's diagnostic policy.

Prometheus independently ages the actual activity timestamp, so frozen cached
text cannot keep liveness healthy. Scrape/evaluation/hold-time delays still apply.
Alert disappearance after a newer result, restart or lost evidence does not prove
recovery; inspect worker-evidence and health/down/absence conditions plus retained
work. These worker alerts retain no durable incident identity and trigger no
automatic scheduling, callback, budget change or fence takeover. Hosted runtime
qualification remains pending for clocks, overlapping calls, cancellation,
scraping and notification transitions.

## Node worker hosts and cleanup

Use the Node combined host and alternative worker scrape job on loopback 9466.
Supervise health polling, HTTP and actual recovery scheduling independently;
neither a health read nor a scrape refreshes worker activity. Join all admitted
work before closing resources or reusing an owner stream.

Node's `cleanupFailures` is a latest-pass gauge. Failed synchronous pool release
may overlap a known settlement, and typed cleanup uncertainty may also overlap
that settlement in `commitUnknown`. Keep the original typed progress/error;
do not impose Go's exclusive outcome sum or infer another delivery from cleanup.
Check the pool's release/discard contract and bound acquisition, driver and
cleanup operations. Unknown COMMIT retains precedence. Preserve saved intent
decisions and reconcile conditionally before scheduling another pass.

Missing cleanup series on Go is expected. Missing Node progress or invalid timing
is unknown evidence, never a zero failure count. A newer result, restart or alert
disappearance does not prove callback confirmation or backlog recovery. These
alerts grant no budget, replay or fence-takeover authority. Hosted qualification
for cleanup uncertainty and joined cancellation remains pending.
