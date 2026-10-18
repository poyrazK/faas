# Prepared event worker monitoring

This opt-in pack connects the [Node](../../docs/event-acknowledgment-node-prometheus.md)
and [Python](../../docs/event-acknowledgment-python-prometheus.md) SDK Prometheus
formatters to an application-owned Prometheus and Grafana installation. Both
produce the same metric names, fixed labels and cumulative seconds histograms.
One shared pack provides seven recording rules, nine warning alerts and a
twelve-panel dashboard for either SDK or a mixed deployment. Nothing is deployed
or enabled by importing an SDK or example.

For retained due/stalled/parked callback backlog, use the separate
[Node recovery-health pack](#recovery-health-node) below.

## Choose the worker host

| SDK | Observer and scrape adapter | Configuration example |
| --- | --- | --- |
| Node | `onEvent: metrics.record`, then the [explicit Node scrape server](../event-acknowledgment-node/README.md#expose-preparation-metrics-to-prometheus) | [prometheus.yml](prometheus.yml), retaining the existing `gregale-event-preparation` job name. |
| Python | `on_event=metrics.record`, then the [explicit same-loop ASGI scrape app](../event-acknowledgment-python/README.md#expose-preparation-metrics-to-prometheus) | [prometheus-python.yml](prometheus-python.yml), using `gregale-event-preparation-python`. |

Both examples target `127.0.0.1:9464` in the worker's network namespace. Choose
one scrape job per collector. For Node and Python hosts sharing one network
namespace, give each listener a different port and update the corresponding
target; for example, keep Node on 9464 and bind Python's ASGI host on 9465. Merge
the distinct scrape jobs into one configuration with one copy of the rules.
Never scrape the same collector under both jobs: that duplicates observations
under different target identities.

Python's ASGI app must share the processing event-loop thread and collector in
one process, with HTTP-only lifespan handling disabled as documented. A separate
server process would have a different in-memory collector. If other threads
access a collector, the host must serialize all record/snapshot calls. Keep one
collector alive across jobs and scrapes. Shared metric names do not make the
SDKs' protected job records interchangeable; preserve each original mode/version.

## Install explicitly

1. Create one long-lived preparation collector and wire its bound `record` to
   the prepared jobs. Expose it with the SDK-specific private scrape example above.
   Use one collector with the default formatter prefix per target.
2. Copy `preparation.rules.yml` to a directory readable by your Prometheus.
   Merge the `rule_files` and selected SDK's `scrape_configs` entries into
   your existing configuration, preserving your other scrape jobs and rule files.
   Relative rule paths resolve from the Prometheus configuration directory.
3. Attach `gregale_event_preparation: 'true'` as a static target label on every
   intended worker target. The rules and dashboard selectors require this label.
   The loopback address works only in the worker's network namespace. For remote
   Prometheus, use your authenticated proxy or protected transport and configure
   its target, scheme and authentication through your own secret management.
4. Lint the rendered configuration before reloading it through your existing
   deployment process:

   ```sh
   promtool check rules preparation.rules.yml
   promtool check config prometheus.yml
   promtool check config prometheus-python.yml
   ```

   Check the configuration you actually install; the commands above cover both
   repository examples. Preserve existing global settings and load the shared
   rule file once, including when both SDKs run in the same evaluation domain.

5. Import `preparation-dashboard.json` in Grafana and select your Prometheus
   datasource. Its UID is `gregale-event-preparation`. Use the Job and Instance
   selectors to retain worker scope. Derived panels require the recording rules
   in this pack; importing JSON alone does not create those rules.
6. Route `family="event_preparation"`, `severity="warn"` through your own
   Alertmanager policy. The example config intentionally supplies no notification
   destination. Alert names and runbook links are listed below.

The files are application examples, separate from platform fleet provisioning.
No Ansible template, fleet alert route, datasource or deployment is modified.
Load one copy of this pack per evaluation domain; duplicate rule/group names or
multiple producers for the same recorded label set can corrupt its interpretation.

## Existing Node installations

The pack moved from `examples/event-acknowledgment-node/monitoring/` to this shared
directory. Existing copied rule/config filenames, alert names, recording aliases,
the default job name and dashboard UID remain the same. The dashboard version is
now 2; its queries, thresholds and panel identities are unchanged. Update source
copy paths to this directory and use one shared rule group per evaluation domain.
The old README links here for existing bookmarks. No reload or deployment occurs
as part of this repository change.

## Scope and tuning

The pack assumes a single Prometheus evaluation domain and unique `(job,
instance)` pairs across the intended targets. Recording rules preserve those
labels, plus outcome, lease/reason or stage where relevant. If a central store
combines multiple Prometheus sources with overlapping target pairs, add stable
source labels to every grouping/matching expression and corresponding dashboard
selectors before using this pack there. Job/instance filters are not tenant
authorization; use a datasource and Grafana access policy appropriate to the
application's monitoring scope.

All raw metric names use the formatter's default
`gregale_event_acknowledgment_preparation_` prefix. If the application uses a
custom prefix, update every raw prefix occurrence in both the rules and dashboard.
Recording aliases begin with `gregale_event_preparation:` and remain stable.
Keep the `gregale_event_preparation="true"` opt-in label out of business inputs.

Each rule group evaluates every 30 seconds. Derived observations use a fixed
ten-minute window and require `up == 1`, a present zero collector saturation flag
and no observed clipping in that window. Failure ratio and p95 recordings also
require at least twenty estimated observations. Ratios use failed shutdowns over
all shutdown outcomes, including cancelled and unknown outcomes in the denominator.
Quantiles use separate callback-settlement and operation-shutdown histograms.

| Alert | Example condition | Hold time | Recovery section |
| --- | --- | --- | --- |
| `GregaleEventPreparationCommitUnknown` | Any positive unknown-commit increase, by stage | None | [Uncertain commits](../../docs/runbooks/GregaleEventPreparation.md#uncertain-commits) |
| `GregaleEventPreparationRenewalFailures` | At least 3 estimated renewal failures, by local/platform lease | 5 minutes | [Renewal failures](../../docs/runbooks/GregaleEventPreparation.md#renewal-failures) |
| `GregaleEventPreparationFailures` | Failed shutdown ratio greater than 10%, at least 20 estimated shutdowns | 5 minutes | [Failed operations](../../docs/runbooks/GregaleEventPreparation.md#failed-operations) |
| `GregaleEventPreparationCallbackSlow` | Callback p95 greater than 30 seconds, at least 20 estimated settlements | 10 minutes | [Latency](../../docs/runbooks/GregaleEventPreparation.md#slow-preparation-or-completion) |
| `GregaleEventPreparationOperationSlow` | Operation p95 greater than 60 seconds, at least 20 estimated shutdowns | 10 minutes | [Latency](../../docs/runbooks/GregaleEventPreparation.md#slow-preparation-or-completion) |
| `GregaleEventPreparationMetricsSaturated` | Collector clipping flag equals 1 | 1 minute | [Observation access](../../docs/runbooks/GregaleEventPreparation.md#clipped-or-missing-observations) |
| `GregaleEventPreparationScrapeDown` | A discovered target has `up == 0` | 5 minutes | [Observation access](../../docs/runbooks/GregaleEventPreparation.md#clipped-or-missing-observations) |
| `GregaleEventPreparationMetricsMissing` | Reachable target omits the collector saturation series | 5 minutes | [Observation access](../../docs/runbooks/GregaleEventPreparation.md#clipped-or-missing-observations) |
| `GregaleEventPreparationTargetsMissing` | No opted-in targets exist | 10 minutes | [Observation access](../../docs/runbooks/GregaleEventPreparation.md#clipped-or-missing-observations) |

These are starting thresholds, not Gregale delivery SLOs. Tune the windows,
minimum observation guards, thresholds and hold times for the workload before
enabling notifications. Keep the dashboard descriptions/thresholds and runbook
in sync. The absence alert assumes at least one expected worker; disable that
specific alert if intentional zero-target intervals are normal. Individual
removed targets need your discovery/inventory health policy when others remain.

## Interpret gaps and estimates

Prometheus `increase` handles visible counter resets and extrapolates over scrape
boundaries, so displayed counts and minimum-volume guards are estimates and can
be fractional. p95 values are histogram estimates over observed settlements;
the largest finite bucket is 300 seconds, so tails beyond it cannot be resolved
accurately. They are not current job budgets or maximum completion times.

An idle reachable exporter still supplies zeroes. With enough scrapes its
increase panels show zero; p95 and failure-ratio panels remain unavailable below
the volume guard. Down, missing or clipped observations leave derived panels
unavailable rather than substituting zero. Clipping can keep them suppressed for
ten minutes after a new unclipped collector appears. A usable window flag means
only that those guards passed, not that all events/scrapes were observed.

First-scrape baselines, crashes, missed scrapes and incomplete shutdowns can lose
events entirely. A removed subset of workers is not covered by the all-targets
absence alert. An old uncertain commit can fall outside the window even when
recovery remains incomplete. Neither alert resolution nor a green scrape proves
delivery success. Keep [Node intent health](../../docs/event-acknowledgment-node-intents.md),
[Python intent health](../../docs/event-acknowledgment-intent-health.md#python-summary)
and [protected processing inspection](../../docs/event-consumer-processing-inspection.md)
as independent evidence. Follow the [SDK-specific recovery paths](../../docs/runbooks/GregaleEventPreparation.md#sdk-recovery-paths).
Metrics never drive automatic recovery here.

The existing static CI checks parse the shared rules and both configuration
examples, parse the dashboard JSON, and verify alert metadata/runbook files. Local validation additionally checks
embedded PromQL and links without evaluating time-series samples.

Local validation covers Prometheus configuration/rule lint, embedded query
parsing, JSON/YAML structure, metadata and documentation checks. No tests or
assertions were added or changed and no local tests were run. Hosted alert
evaluation and Grafana rendering remain unverified before production use.

## Recovery health (Node)

The separate recovery pack observes retained callback backlog, including exhausted
parked intents. It uses Node's
[`EventAcknowledgmentIntentHealthMetrics`](../../docs/event-acknowledgment-node-recovery-prometheus.md),
an application-owned poller and a cache-only scrape handler. Its five recordings,
six warning alerts and nine-panel dashboard do not change the preparation pack.
Python uses the same metric names, rules and dashboard through the
[Python recovery host](#recovery-health-python). Keep SDK-specific host lifetimes
and typed checkpoints separate.

1. Start the [Node recovery metrics host](../event-acknowledgment-node/README.md#export-recovery-health-to-prometheus)
   explicitly with trusted account/app scope and a bounded PostgreSQL pool. The
   example reads at most 1000 unfinished rows, uses a five-minute stall interval,
   polls five seconds after each completed read and allows a thirty-second sample
   age. Each read has a cooperative five-second deadline. Importing starts nothing.
2. Bind its scrape server to loopback port 9466 in the worker's namespace. The
   preparation examples use other ports. Prometheus must share that namespace or
   use an explicitly protected reachable address. Do not expose metrics through
   delivery ingress. Use one owner scope per collector/target and keep authorized
   scope mapping in host configuration, outside metric labels.
3. Copy `recovery.rules.yml` and merge `prometheus-recovery.yml` into application
   Prometheus configuration. Preserve target label `gregale_event_recovery="true"`
   and unique `(job, instance)` identities. Install each rule group once. Check
   syntax with `promtool check rules recovery.rules.yml` and
   `promtool check config prometheus-recovery.yml`, then explicitly reload under
   the application's deployment process.
4. Import `recovery-dashboard.json`, UID `gregale-event-recovery`, and choose its
   datasource/job/instance. Load the recordings first. Counts come from one
   bounded retained scope; duplicate observers cannot be summed into a global
   total. This section creates no deployment, listener or notification route.

All raw names use `gregale_event_acknowledgment_recovery`; custom formatter
prefixes require replacing every raw prefix in rules/dashboard. The aliases
`gregale_event_recovery:*` remain fixed. All metrics are gauges without identity
labels. Before a valid read or after failure, sample gauges are omitted and
`read_success`/`evidence_eligible` are zero. Incomplete/stale observations retain
their sampled raw counts but cannot populate eligible backlog recordings. The
dashboard leaves unknown counts missing and does not interpolate gaps as zero.

The eligible-evidence recording requires `up == 1`, successful recording, complete
fresh advancing evidence and a database timestamp within the exported age bound
relative to Prometheus time. It also checks the exported conservative age. A
stopped poller, frozen snapshot, read failure, invalid/truncated sample or clock
problem therefore cannot establish a healthy backlog. Scrape/evaluation delays
still apply; defaults are fifteen/thirty seconds. Synchronize clocks and tune age
and scan bounds to the workload. This is metadata coverage, not worker availability.

| Alert | Starting condition | Hold time |
| --- | --- | --- |
| `GregaleEventRecoveryParked` | Eligible parked count >= 1 | 5 minutes |
| `GregaleEventRecoveryDueBacklog` | Eligible due count >= 100 | 5 minutes |
| `GregaleEventRecoveryStalled` | Eligible stalled count >= 1 | 5 minutes |
| `GregaleEventRecoveryEvidenceUnavailable` | Reachable target without eligible evidence | 2 minutes |
| `GregaleEventRecoveryScrapeDown` | Discovered target has `up == 0` | 5 minutes |
| `GregaleEventRecoveryTargetsMissing` | No opted-in target exists | 10 minutes |

These are configurable starting thresholds, not delivery SLOs. Disable the absence
alert if intentional zero-target intervals are normal; removed subsets need an
inventory policy. All alerts link the
[recovery runbook](../../docs/runbooks/GregaleEventRecovery.md), including exact
guarded budget-resume commands and retained command identity after uncertainty.
No alert resumes an intent or chooses a business/negative callback action.

Prometheus backlog alerts can stop matching when evidence disappears and issue
resolved notifications. That does not prove recovery: inspect unavailable/down/
absence alerts and authoritative rows. A budget grant or newer staging can lower
parked count without callback confirmation. Use the
[checkpointed watch](../../docs/event-acknowledgment-node-checkpoints.md) for stable
incident identity retained across unknown evidence and restart.

CI statically checks rules/config, dashboard JSON and alert metadata. Local query
parsing does not evaluate time series. Live scraping, alert transitions, notification
routing and Grafana rendering require hosted qualification before production use.

## Recovery health (Python)

Use the [Python cache/formatter and explicit poller](../../docs/event-acknowledgment-python-recovery-prometheus.md)
with the [same-loop ASGI host](../event-acknowledgment-python/README.md#export-recovery-health-to-prometheus).
Its default scan, stall, poll, query and freshness budgets match the Node example.
Keep the cache, poller and ASGI application in the same process/event-loop thread.
A separate Uvicorn process or worker would hold a different cache. Serialize any
cross-thread access explicitly, disable ASGI lifespan and join polling before
closing database resources. Reads require fresh exclusive idle autocommit async
connection scopes and bounded driver/acquisition/cleanup calls.

Copy [prometheus-recovery-python.yml](prometheus-recovery-python.yml), using job
`gregale-event-recovery-python` and loopback port 9467. Node's recovery example
keeps its existing job/9466 target; preparation listeners use other ports. When
combining hosts, merge distinct scrape jobs with one copy of `recovery.rules.yml`.
Never scrape one cache under both jobs or sum duplicate scope observers. The
same `gregale_event_recovery="true"` label, raw metric prefix and recording aliases
apply. Custom prefixes require matching rule/dashboard substitutions.

The dashboard retains UID `gregale-event-recovery`, panel identities, queries and
thresholds; version 2 updates its language description. All five recordings and
six alert expressions/hold times are shared unchanged. Python's native integer
nanosecond freshness handling and exact microsecond timestamp text preserve the
same evidence semantics. Failed/missing reads omit sample gauges, incomplete/
stale observations cannot confirm backlog and unknown panels remain missing.

Use the [Python operator mappings](../../docs/runbooks/GregaleEventRecovery.md#python-hosts-and-guarded-recovery)
for exact budget-resume fences, persisted command identity and uncertain-commit
replay. Shared metrics do not translate protected jobs or checkpoints between
SDKs. No alert starts a recovery pass or grants additional claims.

CI statically checks the Python scrape configuration with the existing shared
rules/dashboard/metadata gates. Local syntax, generation and package checks do
not qualify live ASGI scraping, notification routing or alert evaluation; hosted
behavioral qualification remains pending before production use.

## Recovery health (Go)

Use the [Go concurrent cache, poller and HTTP adapter](../../docs/event-acknowledgment-go-recovery-prometheus.md)
with the [buildable host](../../sdk/go/examples/event-acknowledgment-recovery/README.md#export-recovery-health-to-prometheus).
It uses the same scan, stall, poll, query and freshness defaults as Node/Python.
Mutex-protected state supports concurrent polling/scrapes, and each scrape copies
its sample before formatting. The host explicitly supervises polling and its
separate unbound `net/http` server. Cancel/join polling and shut down/join HTTP
before closing the configured database pool; bound actual driver operations.

Copy [prometheus-recovery-go.yml](prometheus-recovery-go.yml), using job
`gregale-event-recovery-go` and loopback port 9468. Existing Node/Python configs,
jobs and ports remain usable. Combine distinct jobs with one copy of the shared
rules; never scrape one cache under multiple jobs or sum duplicate owner scopes.
The opt-in target label, thirteen raw gauges, five recordings, six alert meanings
and hold times are identical. Owner mapping remains in trusted host configuration.

Dashboard version 3 adds Go to the description while preserving its UID, panels,
queries and thresholds. Go uses UTC database timestamps, conservative wall/local
monotonic ages, independent snapshots and exact fractional Unix timestamp text.
Failed reads omit sample gauges; stale/incomplete samples stay ineligible and
backlog panels remain Unknown when evidence is missing. Use
[Go operator mappings](../../docs/runbooks/GregaleEventRecovery.md#go-hosts-and-guarded-recovery)
for precise guarded resume/adoption and unknown-COMMIT replay. Shared metrics do
not translate protected jobs or typed SDK checkpoints. No alert grants claims.

CI statically checks the Go scrape configuration alongside the shared monitoring
assets. Local builds/vet and static checks do not qualify concurrent HTTP scraping,
driver cancellation, live alerts or Grafana rendering; hosted behavioral
qualification remains pending before production use.

## Actual recovery-worker activity (Go)

The [Go worker observer](../../docs/event-acknowledgment-go-worker-liveness.md)
records actual bounded pass start/joined return independently from health polling.
Use the [combined host](../../sdk/go/examples/event-acknowledgment-recovery/README.md#observe-actual-recovery-worker-activity)
and [worker scrape job](prometheus-recovery-go-worker.yml) instead of the health-only
job for the same 9468 target. Explicitly schedule passes through the monitored
wrapper and supervise health polling/HTTP separately. Neither construction nor
scraping starts recovery work.

The additional `gregale_event_recovery_worker="true"` opt-in leaves existing Go,
Node and Python health-only jobs compatible. Keep the original recovery opt-in
label for existing down/absence/backlog alerts. Never scrape the same cache with
both jobs or sum duplicate observers. Missing subsets require expected inventory.

Three worker recordings and three warning alerts are separate from the original
five health recordings and six alerts. Worker evidence requires reachable opted-
in timing/clock metadata; recorded age is the greater of host conservative age
and Prometheus time since actual activity. Healthy scraping or database reading
cannot refresh that timestamp. Default idle/active expectations are one/five
minutes, followed by a two-minute alert hold; these are not deadlines or SLOs.

| Alert | Condition held two minutes |
| --- | --- |
| `GregaleEventRecoveryWorkerOverdue` | First/next-pass idle age or active-pass duration exceeds its expected bound. |
| `GregaleEventRecoveryWorkerEvidenceUnavailable` | Reachable opted-in target lacks valid worker timing/clock metadata. |
| `GregaleEventRecoveryWorkerPassFailed` | Latest valid joined pass retains failed or unknown-COMMIT admissions. |

Dashboard version 4 keeps the original nine health panels and adds eight worker
panels for state, age/bounds, latest duration, start/return times and scalar pass
outcomes. These outcomes are latest-pass gauges, not cumulative counters/rates
or current database backlog, and may describe the previous pass while another
is active. Missing data stays Unknown. Callback confirmation/business success
cannot be inferred from a completed/empty pass or a resolved notification.

See the [worker runbook](../../docs/runbooks/GregaleEventRecovery.md#worker-liveness)
for overlap protection, clock repair and unknown-COMMIT handling. Alerts start no
recovery, grant no claims and retain no durable incident identity. Static CI
checks cover the opt-in config, shared rules, dashboard and metadata. Hosted
behavioral qualification for clocks, concurrency, cancellation and live alerts
remains pending. Python worker-metric parity can follow independently.

## Node worker activity and cleanup

The [Node actual-pass observer](../../docs/event-acknowledgment-node-worker-liveness.md)
uses the same 23 worker gauges and adds `last_pass_cleanup_failures`. Use
[prometheus-recovery-node-worker.yml](prometheus-recovery-node-worker.yml) with
the combined Node host on 9466 instead of the health-only job for that endpoint.
Both recovery and worker opt-in labels are required. Keep one target per cache.

The shared pass-failure alert also includes Node pool-cleanup failures. Such
failures and typed commit uncertainty can overlap a known settlement; preserve
the original progress/error and never sum all outcomes into deliveries. Dashboard
version 5 adds Node wording and the cleanup series to the worker outcomes panel.
The original nine health panels remain unchanged; cleanup stays Unknown on Go
targets. See the [Node cleanup runbook](../../docs/runbooks/GregaleEventRecovery.md#node-worker-hosts-and-cleanup).
Hosted behavioral qualification remains pending.
