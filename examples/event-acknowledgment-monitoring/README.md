# Prepared event worker monitoring

This opt-in pack connects the [Node](../../docs/event-acknowledgment-node-prometheus.md)
and [Python](../../docs/event-acknowledgment-python-prometheus.md) SDK Prometheus
formatters to an application-owned Prometheus and Grafana installation. Both
produce the same metric names, fixed labels and cumulative seconds histograms.
One shared pack provides seven recording rules, nine warning alerts and a
twelve-panel dashboard for either SDK or a mixed deployment. Nothing is deployed
or enabled by importing an SDK or example.

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
