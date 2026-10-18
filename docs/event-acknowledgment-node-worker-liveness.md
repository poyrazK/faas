# Node recovery-worker liveness

`EventAcknowledgmentIntentRecoveryWorkerMetrics` observes actual bounded recovery
passes separately from database-health reads and HTTP scrapes. It implements
[Go's 23 worker gauges](event-acknowledgment-go-worker-liveness.md) with one extra
Node pool-cleanup gauge, preserving Node's existing overlapping outcome semantics.
Constructing/importing starts no SQL, callback, task, listener or timer.

## Observe explicit passes

```js
import {
  EventAcknowledgmentIntentRecoveryWorkerMetrics,
  recoverEventAcknowledgmentIntentsWithMetrics,
  formatEventAcknowledgmentIntentRecoveryWorkerMetrics,
} from '@gregale/sdk-node';

const worker = new EventAcknowledgmentIntentRecoveryWorkerMetrics(accountId, appId, {
  maxIdleMs: 60000, maxPassDurationMs: 300000,
});
// Called explicitly by the application scheduler; keep the pool/client available.
const result = await recoverEventAcknowledgmentIntentsWithMetrics(
  pool, accountId, appId, worker, { signal, maxAttempts: 100, concurrency: 4 },
);
// Inspect the original result/typed exception before deciding whether to run again.
// A separately supervised HTTP handler formats a fresh snapshot per scrape:
const body = formatEventAcknowledgmentIntentRecoveryWorkerMetrics(worker.snapshot());
```

The wrapper validates the existing pass policy, pool, normalized owner and native
cache brand before I/O. Options, retry policy and callback settings are copied/
frozen before the first await; unknown fields, symbols, selected accessors and
malformed values fail with fixed `TypeError` text. Scope mismatch or a second
pass using the same cache also fails before I/O. Use one stable cache per trusted
scope and nonoverlapping scheduled pass stream on the same event-loop thread.
Separate workers/processes hold separate caches and need separate targets.

`maxIdleMs` accepts whole milliseconds 100..86400000, default 60000;
`maxPassDurationMs` accepts 1000..86400000, default 300000. Omission selects the
default; zero is invalid. These are observations, not operation deadlines or
scheduling authority. Configure actual pool acquisition/query/synchronous-release
and platform callback bounds separately. The host owns application scheduling.

The wrapper calls the existing [bounded Node pass](event-acknowledgment-node-recovery-pass.md).
Normal return preserves the original frozen result. The existing typed ordinary
failure, unknown-COMMIT and native cancellation exceptions are rethrown unchanged,
after their joined progress is copied into the cache. Pre-aborted valid calls
retain the existing interrupted zero-attempt exception and do not refresh worker
activity. The cache retains no raw exception, caller abort reason or driver cause.
No promise race, force-cancelled worker or abandoned operation is introduced.

Unknown non-pass rejections leave activity in flight because joined completion
cannot be established. Investigate the original work/pool contract; never replace
the cache merely to bypass overlap protection. Likewise an invalid clock during
completion marks timing invalid without replacing the original pass result/error.
Cancellation remains cooperative: join the pass before closing pools/clients or
the event loop. Metrics never authorize a callback, business replay or fence takeover.

## Timing and native progress

Construction starts the first-pass idle expectation. Actual pass start and joined
return are the only later activity updates. Idle age measures construction/latest
return to now; active age measures the current pass start to now. A health query,
scrape or unmonitored pass cannot update that reference. Last-start/return fields
are omitted until observed. Latest joined progress remains historical while a
new pass is active. No completion or zero outcome is fabricated for missing data.

Ages take the greater of wall-clock and local monotonic elapsed time; duration
uses local monotonic time. Detected regression makes `clockValid=false` for the
cache's lifetime, requiring clock review before explicit replacement. Waiting
cannot repair invalid clock evidence or manufacture completion. `overdue` is
meaningful only with valid timing; shared rules separately gate that evidence.

Result validation copies only own bounded native scalar fields and a whitelisted
stop reason, then freezes them. Counts cannot exceed 10000 admissions. Known
primary outcomes cannot exceed admissions; uncertainty may overlap a known
outcome only within the reported cleanup-failure/uncertainty bounds. Missing
admission coverage or malformed progress omits outcome evidence while retaining
known joined timing. Snapshots and nested results are detached/recursively frozen,
with no owner/invocation identity, protected callback, payload, connection or error.

Node's `cleanupFailures` counts admissions with failed synchronous pool release.
It can overlap a known settlement; typed cleanup uncertainty can also overlap
that settlement in `commitUnknown`. Do not impose Go's mutually exclusive count
sum on this native result, sum all outcome fields into deliveries, apply `rate()`
or sum successive samples. All outcome metrics are latest-pass gauges. Preserve
the original SDK result/error and uncertainty precedence when scheduling again.
Known parked outcomes are not current retained parked backlog. Empty/limit stop
and joined return do not prove global drain, callback or business success.

## Exposition and explicit host

`formatEventAcknowledgmentIntentRecoveryWorkerMetrics` is a pure text-0.0.4
formatter. The 23 shared suffixes, HELP strings and order match
[Go's gauge table](event-acknowledgment-go-worker-liveness.md#metrics-and-http).
The appended `last_pass_cleanup_failures` gauge appears only with valid progress.
All 24 families are gauges without generated labels or explicit sample timestamps;
use `EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE` for HTTP.

The default prefix is `gregale_event_acknowledgment_recovery_worker`. Optional
`{ prefix }` must be an ASCII identifier 1..128 characters, starting with a letter
or underscore, containing only letters/digits/underscores. Explicit null/empty
values are invalid. Snapshots/options require selected own data properties; no
getters are invoked. Malformed input throws fixed `TypeError` before returning
text. Recompute each scrape; never cache formatted evidence indefinitely.

The [same-loop host example](../examples/event-acknowledgment-node/README.md#observe-actual-recovery-worker-activity)
composes the existing health poller and an unbound HTTP server with the new
worker cache. It replaces its newly constructed server's health-only request
callback before exposing that server; existing health-only examples are unchanged.
Explicitly supervise `runHealth(signal)`, HTTP serving and scheduling of
`recover(signal)`. GET /metrics combines two fresh snapshots before returning
200 with no-store headers; other paths/queries return 404, other methods 405 and
invalid exposition generic 503. No SQL or recovery occurs in that handler.

Bind the server explicitly to loopback 9466, instead of the health-only Node host
for that endpoint. The example sets bounded HTTP timeouts; the application owns
network authorization, protected owner mapping and driver/callback bounds. Stop
admission, cancel/join active recovery and health polling, close/join HTTP, then
close pool/client resources. Do not overlap an unreturned pass or blindly loop
after unknown COMMIT. Shared metrics do not translate protected SDK jobs/checkpoints.

Use [prometheus-recovery-node-worker.yml](../examples/event-acknowledgment-monitoring/prometheus-recovery-node-worker.yml)
instead of the health-only job for the same cache. It uses a distinct job name
with both recovery and worker opt-in labels; Go's worker target stays unchanged.
Keep one target per observer; duplicate scope observers cannot be summed globally.
The shared three worker recordings independently age actual activity with
Prometheus time and preserve missing evidence. Existing health-only targets stay
compatible. Scrape/evaluation delays apply; clocks must be synchronized.

The same overdue/evidence/pass-failure alerts apply. Pass failure also includes
Node cleanup failures, even after a known settlement; interruption alone does
not fire it. Dashboard version 5 adds Node wording and a cleanup series while
preserving the original nine health panels and other queries/thresholds. Cleanup
remains absent/Unknown on Go targets. A newer pass, restart or lost evidence can
stop an alert matching without proving recovery. See the
[Node runbook](runbooks/GregaleEventRecovery.md#node-worker-hosts-and-cleanup).

Generation, build, packaging and static checks do not qualify live concurrency,
overlapping calls, cleanup uncertainty, cancellation, HTTP scraping, clock changes
or alert transitions. Hosted behavioral qualification remains pending before release.
