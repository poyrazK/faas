# Prepared event worker recovery

Source: [opt-in preparation monitoring rules](../../examples/event-acknowledgment-monitoring/preparation.rules.yml).
The [shared Node/Python setup guide](../../examples/event-acknowledgment-monitoring/README.md)
defines thresholds, scope and observation guards. All nine alerts share this
runbook and carry `family=event_preparation`, `severity=warn`.

## Establish scope and evidence

Identify the `job` and `instance` in the alert and select the same worker in the
Grafana dashboard (`gregale-event-preparation`). An all-targets absence alert has
no affected instance; inspect expected discovery configuration first. Verify the
target label, metric prefix and application-owned collector wiring. Restore
observation access before treating absent derived metrics as healthy.

Metrics describe accepted process-local observations, including duplicate events.
They identify no delivery or consumer and contain no token, event, prepared value
or result. Use the application's protected job ownership/diagnostic path to find
affected jobs around the observed time. Authenticate and authorize that access;
keep original records, tokens and payloads out of monitoring labels, annotations
and exported diagnostic logs.

Read receipt/claim metadata through the SDK-specific inspection path below,
using trusted owner scope and the retained exact original inputs. Read durable
intent status through its health and listing APIs.
An unavailable database observation is unknown evidence, not an absent receipt.

## SDK recovery paths

Use the SDK and original protected-job mode that produced the retained job.
The shared monitoring contract grants no permission to convert job records,
acknowledge jobs, run business code or choose a negative acknowledgment.

For explicitly scheduled recovery of saved callbacks, use the bounded
[Node pass](../event-acknowledgment-node-recovery-pass.md) or
[Python pass](../event-acknowledgment-python-recovery-pass.md). Inspect their
aggregate progress after owned work joins. Empty probes do not establish a drained
backlog, and uncertainty takes precedence over cancellation. No alert starts a
pass or authorizes job mutation. Node's unsupported-generation code requires a
compatible recovery host, preserving the retained row's full lineage.

For hosts using Go's application-owned callback store, the
[Go pass](../event-acknowledgment-go-recovery-pass.md) provides the same finite
admission model with `database/sql`, joined context cancellation and scalar
progress. Preserve committed unconfirmed settlements and check unknown-commit
sentinels before cancellation or ordinary failures.

| Operation | Node | Python |
| --- | --- | --- |
| Read receipt/claim metadata | [`inspectEventConsumerProcessing`](../event-consumer-processing-inspection.md) | [`ainspect_event_consumer_processing`](../event-consumer-processing-inspection.md#python-inspection) |
| Validate a saved receipt and stage its completion intent | [`worker.saveCompletionIntent`](../event-acknowledgment-node-intents.md) | [`worker.save_completion_intent`](../event-acknowledgment-python-intents.md) |
| Read bounded retained backlog health | [`getEventAcknowledgmentIntentHealth`](../event-acknowledgment-node-intents.md) | [`aget_event_acknowledgment_intent_health`](../event-acknowledgment-intent-health.md#python-summary) |
| List retained intent metadata | [`listEventAcknowledgmentIntents`](../event-acknowledgment-node-intents.md) | [`alist_event_acknowledgment_intents`](../event-acknowledgment-python-intents.md) |
| Deliver one saved callback without business work | [`recoverNextEventAcknowledgmentIntent`](../event-acknowledgment-node-intents.md) | [`arecover_next_event_acknowledgment_intent`](../event-acknowledgment-python-intents.md) |

### Python job and connection ownership

For version-5 `python-prepared-durable` jobs, use the existing
[`inspect_owned_prepared_delivery`](../../examples/event-acknowledgment-python/README.md#inspect-a-processing-job)
wrapper under the application's diagnostic authorization and job-owner policy.
It preserves record/scope validation and performs metadata inspection without
acknowledging the job. The public inspector itself needs no platform client or
acknowledgment token. Supply a separately leased idle autocommit async psycopg
connection with bounded acquisition/query/statement calls, against the authoritative
application database.

Inspect the frozen snake-case `state`, `receipt`, `claim`, `observed_at`,
`expires_at` and `remaining_lease_ms` evidence. Use the claim/state classification:
an active claim can have zero whole milliseconds remaining. Absent receipts and
expired claims establish neither rollback nor stopped work, and inspection
validates no saved result bytes. Unavailable evidence remains unknown.

For a matching receipt, the original owner can use
`save_owned_prepared_completion_intent` on a fresh connection to validate the saved
result and stage callback recovery without preparation, new claims or business
code. Preserve the existing ownership fence before acknowledging the ingestion
job; a diagnostic snapshot alone never permits deletion. The recovery host then
uses the saved callback through `arecover_next_event_acknowledgment_intent` or
`recover_one_saved_callback`, with its own exclusive connection and owner policy.
Use health/listing independently to inspect unfinished retained intents.

For an owner-scheduled catch-up of already saved callbacks, the explicit
[bounded Python recovery pass](../event-acknowledgment-python-recovery-pass.md)
adds controlled concurrent iterations and exclusive connection scopes. Inspect
its frozen aggregate result after all work/cleanup joins. Empty probes do not
prove the backlog drained, and uncertain COMMIT remains its existing error
category even during cancellation. The pass neither acknowledges ingestion jobs
nor executes business work; an alert never starts it automatically.

An uncertain COMMIT closes the exclusively owned connection; discard it through
the pool and reconcile on a fresh idle autocommit connection. Cancellation joins
active SQL and cleanup, so bound driver calls and drain operations before closing
pools/clients/event loops. Never reinterpret a new diagnostic read as proof that
the old transaction rolled back. If restart is explicitly authorized by the job
owner, the original `run_prepared_durable` coordinator and live platform/local/
receipt fences still decide admission. The alert selects no restart or failure
decision. See the [Python preparation lifecycle](../event-acknowledgment-python-preparation.md).

## Uncertain commits

`GregaleEventPreparationCommitUnknown` reports a positive estimated increase in
unknown local commits over ten minutes, split by lifecycle stage. It can include
uncertain claim/renewal transactions as well as final completion. It does not
establish that an observed transaction remains unresolved. Resolution of the
alert only means the expression no longer holds.

Preserve the owned job's original mode and immutable inputs. Inspect the
authoritative application database and retained intent evidence before selecting
an owner-controlled recovery action:

| Evidence | Action |
| --- | --- |
| Matching completed receipt | Use the SDK's receipt reconciliation path above to validate the saved result and stage completion without invoking business work. Recover the saved callback separately. |
| Matching active processing claim | Respect the live fence and job ownership. A snapshot supplies no permission to take over or extend the claim. |
| Expired/absent claim and no observed receipt | Reconcile uncertain transactions, retention and exclusive job ownership before an explicit restart through the original coordinator path. Live claim/platform/receipt fences still decide admission. |
| Identity/input conflict | Preserve the evidence and investigate the immutable disagreement. Keep conflicting work out of automatic recovery. |
| Storage or diagnostic failure | Restore access and inspect again; missing evidence cannot decide commit outcome. |

A matching receipt observation does not validate the saved response bytes; the
ordinary completion path does that. An absent receipt does not prove rollback,
and an expired claim does not prove the old worker stopped. Preserve receipts,
claims and intent history. Explicit negative decisions require stopped work,
known rollback and exclusive ownership through the original job protocol.

## Renewal failures

`GregaleEventPreparationRenewalFailures` reports at least three estimated renewal
failure observations per lease in a rolling ten-minute window, held for five
minutes. Use the dashboard's fixed `reason` breakdown to distinguish causes.

For `lease=local`, inspect application PostgreSQL reachability, pool/query
timeouts, statement latency, event-loop delays, processing lease settings and
the retained claim fence. For `lease=platform`, inspect platform reachability,
attempt expiry/supersession and confirmed renewal responses. The leases protect
different owners and one successful renewal does not establish the other's health.

Follow the existing coordinator cancellation and transaction cleanup path.
Inspect receipts/intents after uncertain outcomes. A late platform renewal
failure can coexist with a confirmed local completion; callback recovery remains
the durable intent owner's task. Repeated lease failure is not permission to
delete a fence or replay external effects.

## Failed operations

`GregaleEventPreparationFailures` means more than ten percent of observed
shutdowns are `failed` over ten minutes with at least twenty estimated shutdowns,
held for five minutes. All shutdown outcomes form the denominator. Cancelled
and unknown-commit outcomes remain separate on the dashboard.

Inspect bounded application diagnostics for preparation errors, timeout, claim
contention, immutable conflicts and local storage failures. A returned callback
may still be followed by a failed final transaction. Local commits, preparation
settlements and shutdowns overlap; adding them together does not count deliveries.
Correct the underlying dependency or implementation, then apply the original
durable job-owner recovery policy using retained evidence.

## Slow preparation or completion

`GregaleEventPreparationCallbackSlow` uses callback-settlement p95 above thirty
seconds; `GregaleEventPreparationOperationSlow` uses shutdown-duration p95 above
sixty seconds. Each requires twenty estimated observations per ten-minute window
and holds for ten minutes. Callback samples include both returned and thrown
callbacks. Operation samples include startup, claim, preparation, final
transaction and cleanup. These are histogram estimates, not active-work gauges.

Compare the two panels. Investigate preparation dependencies for slow callbacks;
inspect pool acquisition, transaction/lock latency and renewal cleanup when whole
operations are slow. Configure bounded acquisition/query/statement timeouts and
owner deadlines. The preparation timeout does not bound the final transaction.
The largest finite histogram bucket is 300 seconds; larger tails need separate
bounded diagnostics. Tune thresholds to the workload's actual budgets rather
than extending leases solely to silence an alert.

## Clipped or missing observations

- `GregaleEventPreparationMetricsSaturated`: a sticky collector clipping flag
  has been 1 for a minute. Affected values are lower bounds. Inspect collector
  wiring and its observation lifetime; use a controlled host lifecycle to
  replace it if appropriate while preserving durable jobs and ownership. A new
  collector resets counters, not recovery state. Derived windows stay suppressed
  until no clipping has been observed for ten minutes.
- `GregaleEventPreparationScrapeDown`: a discovered endpoint has been
  unreachable for five minutes. Check host/listener health, configured address,
  network namespace, authenticated proxy and scrape transport. Check the last
  scrape error in Prometheus. Reachability alone does not diagnose processing.
- `GregaleEventPreparationMetricsMissing`: a reachable target has omitted the
  expected collector saturation series for five minutes. Check route, formatter
  prefix and use of the same long-lived collector as the prepared jobs. This is
  a presence probe, not validation of every metric family.
- `GregaleEventPreparationTargetsMissing`: all opted-in targets have been
  absent for ten minutes. Check the explicit target label and intended discovery
  configuration. Monitor individual expected target removal through your own
  inventory when other targets remain. Intentional zero-target periods require
  tuning that alert before use.

The dashboard needs the companion recording rules. Low sample volume makes p95
and failure ratios unavailable even when raw counters exist. A usable window
does not guarantee continuous event/scrape coverage; first scrapes, crashes and
gaps can lose observations. Keep durable recovery checks independent. No alert
here acks a job, steals a claim, retries business work or sends a terminal callback.

## Verification limits

Configuration/rule lint, embedded query parsing, JSON/YAML structure and static
documentation checks cover the shipped assets. No tests/assertions were added
or changed and no local tests were run. Hosted rule evaluation, alert routing and
Grafana rendering must be qualified in the application's monitoring environment.
