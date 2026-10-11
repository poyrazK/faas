# Monitor production route budgets

Production route monitoring continues after a release is promoted. It checks
absolute error and latency budgets against stored observations on the sole fully
serving production deployment. Monitoring is advisory by default and independent
of the canary guard and saved contract/policy checks; it can opt into an
[automatic rollback](#automatic-rollback) for early error-budget incidents.

To see every route's canary, production and contract coverage together, run
[`gregale routes status`](route-status.md).


Create a JSON file using exact gateway-normalized method/path labels:

```json
[
  {"method": "POST", "path": "/checkout", "max_5xx_rate_bps": 100, "max_p95_ms": 500},
  {"method": "POST", "path": "/login", "max_p95_ms": 300}
]
```

100 basis points means a 1% 5xx budget. Omission disables error monitoring for a
route; zero selects a zero-error budget. A positive `max_p95_ms` selects latency.
Each route requires at least one budget. Values exactly at a budget are allowed.

Budgets need at least 20 requests per window. A route that is unknown only
because its one-minute windows are too sparse is also judged over the newest 30
minutes since the release or configuration change, split into two halves, with
the same budgets. Such findings report `evidence_window: pooled` and their
`pooled_windows`, and can open incidents and trigger an
[automatic rollback](#automatic-rollback).
Select at most 20 distinct labels, with no wildcards, expanded URLs or queries.

```sh
gregale routes monitor set my-api --mode enabled \
  --routes production-routes.json --expected-revision 0
gregale routes monitor get my-api --json
gregale routes monitor report my-api
gregale routes monitor report my-api --fail-on-unhealthy --json
gregale routes monitor preview my-api --routes production-routes.json --json
gregale routes monitor set my-api --mode enabled --routes production-routes.json \
  --customer-group-by tenant --expected-revision 1
```

Use `routes monitor preview` to evaluate proposed budgets against the latest
closed production windows before saving them. It reports route verdicts,
observed traffic coverage and optional per-customer impact without changing the
saved monitor. Customer IDs stay redacted unless `--customer-details` is
explicitly supplied. Saving changed budgets starts a fresh observation window,
so the preview is evidence about current traffic, not a prediction of the first
post-save report. Add `--fail-on-unhealthy` to use the preview as a CI check.

Updates replace intent and require the current revision. Identical intent is a
no-op. Enabling requires request telemetry entitlement; disabling remains
available after a downgrade. The CLI rejects misspelled budget fields. Reports
are read-only and do not create incidents. The CI flag requires healthy evidence;
unknown, violated and disabled reports exit nonzero with the report printed.

## Customer impact

Customer evaluation is opt-in with `--customer-group-by tenant` or
`--customer-group-by consumer`; omitting the option disables it. Each request is
grouped by the tenant or API-consumer UUID recorded when the request arrived.
The same absolute route budgets and observation windows are then evaluated for
each observed cohort. A sustained violation by even one cohort makes the overall
report violated and opens the normal saved incident, including when aggregate
traffic is within budget. Full-population counts are computed before the report
caps details at five cohorts and one hundred violating UUIDs per route.

Customer UUIDs are redacted in reports, incident history, and CLI output by
default. Pass `--customer-details` to `routes monitor report` or `routes monitor
explain` for an explicit, read-time request to include observed tenant/consumer
UUIDs. These are opaque IDs; the report does not resolve names or claim people,
accounts, or billing counts. It identifies unattributed and unresolved request
weights separately. Incomplete attribution or sparse cohort evidence makes
customer health unknown rather than healthy.

Every customer cohort that violates while an incident is open joins its bounded
recovery inventory. The incident recovers only after each inventoried identity
has comparable healthy windows on the same deployment and configuration. If the
violating identity population exceeds the 100-ID recovery cap, the inventory is
marked incomplete and the incident stays open until an operator changes the
monitor intent; the system never declares recovery while it has forgotten a
known violator. The recovery inventory is kept in the monitor row and cleared
when an incident closes or the configuration changes. Opening incidents also
save bounded request examples scoped to displayed violating cohorts, so an
isolated customer failure remains diagnosable when aggregate route budgets pass.
These entries follow the same three route/signal evidence cap and redact the
customer UUID unless `customer_details=true` is requested.

## Sustained observations

APID polls due monitors every 30 seconds and schedules each successful evaluation
one minute later. It selects the sole default-scope live deployment receiving
100% traffic whose canary steps have completed. Deployments without canary steps
also qualify. Preview traffic and retired deployments are excluded. A split,
missing/ambiguous deployment, lost entitlement or missing observations is unknown.

Two consecutive closed UTC minute windows sit behind a 30-second ingestion
allowance. Both must begin after the latest intent update, deployment creation,
canary stage and recorded rollout completion. Errors require at least 20
represented requests per window and at least two errors to confirm an exceeded
budget. One over-budget error stays unknown. Latency requires 100 represented
requests per window. Both windows must violate the same selected signal to open
an incident; mixed error/latency failures do not establish a sustained violation.

Counts retain publisher aggregation weights. P95 interpolates weighted latency
bucket representatives, including successful responses and cold boot latency.
Coverage is `observed_only`: this does not establish complete capture, an end-user
count or compliance with an SLO. Low-traffic routes may stay unknown.

## Saved incidents and investigation

```sh
gregale routes monitor incidents my-api
gregale routes monitor incidents my-api --limit 5 --before INCIDENT_UUID
gregale routes monitor explain my-api --incident INCIDENT_UUID
gregale routes monitor explain my-api --incident INCIDENT_UUID \
  --out production-incident.json --json
gregale routes impact my-api --base BASE_COMMIT --head DEPLOYED_COMMIT \
  --path services/api --out route-impact.json --json
gregale routes monitor explain my-api --incident INCIDENT_UUID \
  --source-impact route-impact.json --out incident-with-source.json --json
gregale routes monitor explain my-api --incident INCIDENT_UUID \
  --source-impact auto --out incident-with-source.json --json
```

The worker opens one aggregate incident when a budget violation is confirmed.
Repeated violations stay quiet. The opening report fixes deployment/commit,
configuration revision, budgets, anchors, counts and exact windows. It preserves
at most three violated route/signal diagnostic entries in selector order, errors
before latency, and explicitly reports omitted entries. This snapshot describes
the opening evaluation and never changes. While the incident remains open, each
monitor evaluation appends a compact impact snapshot, including unknown results;
the final healthy evaluation is retained before recovery. The timeline records
global and per-route error/latency states plus aggregate observed, violated and
unknown customer counts when cohort monitoring is enabled. It contains no
customer IDs or request details. Route indexes refer to the selector order in
the opening report. The timeline keeps the opening baseline and up to 59 newest
evaluations; the incident's 512 KiB encoded-size limit can shorten that history.
When older observations roll off, `timeline_truncated` is true.
`routes monitor explain` shows route/signal changes and cohort-count changes
between observations. Older incidents without saved follow-up evaluations
remain readable and show only their opening baseline.

When an open incident gains a newly violated route signal, the worker saves a
separate escalation snapshot keyed by the webhook's `transition_id`. It records
the newly affected route indexes and signal verdicts plus up to three fresh
aggregate request/latency diagnostic entries from that evaluation. The opening
snapshot remains unchanged, repeated violations do not create duplicate
snapshots, and escalation evidence never includes customer identities. Up to 20
recent transitions are retained; `escalations_truncated` and each transition's
`evidence_truncated` flag report history or diagnostic caps. `routes monitor
explain` prints the transition ID, newly violated routes/signals, and captured
evidence so an operator can connect a webhook directly to its saved diagnosis.

For incident triage, pass a local `routes impact` report to `routes monitor
explain --source-impact`, or use `--source-impact auto` to have the CLI generate
that report from the current local Git checkout. Auto mode reads the baseline
and candidate revisions from the saved incident, using the baseline's stored
repository-relative source root; it does not clone or fetch source. The CLI
checks the incident deployment's app and commit and the report base against the
saved last-known healthy deployment, then compares affected method/path
selectors with the static route inventory.
The baseline is retained only after a fully serving deployment receives a
healthy monitor report, and a different deployment is snapshotted into
incidents when they open.
Whole-segment parameter names can differ when the mapping is unique;
unsupported, ambiguous and unreported routes remain explicit. Matched routes
show source-change classifications, handler locations and bounded static
reference chains next to the saved incident evidence. It also includes aggregate
observed, violated and unknown customer counts for each affected route when
that route has cohort data; customer identities are not added to the source
correlation. When the local checkout's origin matches the incident repository,
the CLI reads CODEOWNERS from the saved candidate commit and shows the matching
owner rule for each changed or referenced source file. It follows GitHub's
CODEOWNERS file priority and last-matching-rule behavior. Unowned paths,
unsupported source paths, missing CODEOWNERS data, and repository mismatches
remain visible without guessing. This is a local handoff hint: it sends no
notifications, and the owner list does not prove who is available or responsible
for a runtime regression.

The report's base and
candidate revisions, repository and source root must agree with the saved
healthy baseline and incident deployment's declared metadata. Older incidents
and incidents without a distinct prior healthy deployment cannot produce a release-pair
correlation. These metadata checks do not
verify source bytes against the deployment archive, and static references are
possible-impact leads rather than proof of execution or root cause. Without
`--source-impact`, the incident response and output format are unchanged.
If the local repository or either revision cannot be analyzed, the explanation
still succeeds and source correlation reports `local_repository_unavailable`
or `local_analysis_unavailable`.

Each diagnostic window captures up to three matching retained request references.
Error examples select all 5xx; latency examples include all statuses and prefer
the slowest buckets. Matching row and represented-request totals precede the cap.
Latency also captures an independent newest-32-row sample, normalized dependency
span timings, measured guest duration and available wake boot timing using the
same bounds as [latency investigation](route-investigation.md#investigate-a-slowdown).
In saved diagnostic objects, `candidate` denotes the monitored deployment;
`stable` is empty, dependency groups are one-sided, and no deltas are claimed.
Samples, missing spans and omissions remain explicit. These separate percentiles
cannot be added and do not establish root cause.

Human output links to existing request inspection commands. The JSON retains
redacted metadata and dependency summaries, not SQL, names, raw destinations,
request bodies or headers. Original trace/log contents may expire independently;
following a debugger link rechecks current entitlement, authorization and request
retention. Saved evidence requires current telemetry entitlement, app-read scope
and completed MFA. `--out` creates an owner-only file and refuses existing files
and symlinks.

An incident recovers only when all selected budgets have comparable healthy
windows on the same deployment, revision and anchors, including the customer
identities that violated during the incident. The recovery report is appended
while opening evidence stays fixed. Unknown data never closes an
incident. Intent edits/disable and a new fully serving deployment mark the old
incident `superseded`, without claiming health recovery. During an ambiguous
split the previous incident remains open. Recurrence after recovery creates a new
incident. History retains the active incident plus up to 100 newest closed
incidents within 8 MiB; entries are capped at 512 KiB. Pruned IDs/cursors return
not found.

## Automatic rollback

Monitoring only reports by default. To revert a release that breaks a selected
route soon after it reaches full traffic, opt in with `--on-violation rollback`:

```sh
gregale routes monitor set my-api --mode enabled --routes production-routes.json \
  --on-violation rollback --expected-revision CURRENT_REVISION
```

Rollback mode requires at least one route with `max_5xx_rate_bps`; latency
budgets keep reporting but never trigger a rollback. Gregale decides once per
incident, using its saved opening evidence:

- **Requested** when an error budget is violated and the incident opens within
  30 minutes of the deployment's last traffic change (rollout completion or
  promotion). Gregale requests a checked rollback to the incident's saved
  healthy baseline, the same operation as `gregale rollback --to ... --expected-current ...`, so the
  artifact check, production contract gate and binding checks still apply.
- **Skipped** when only latency was violated (`latency_only_violation`), the
  incident opened later than 30 minutes after release
  (`outside_rollback_window`), no healthy baseline was saved
  (`no_healthy_baseline`), or the checked rollback could not be requested
  (`rollback_target_ineligible`).
- **Not decided** while a rollout, another rollback or a traffic split owns the
  deployment.

`gregale routes monitor explain my-api --incident INCIDENT_UUID` prints the
decision and the rollback operation ID. Follow it with
`gregale rollback status my-api --operation OPERATION_UUID`.
The audit log records `route_monitor.rollback_requested`. Switching back to
`--on-violation report` stops new decisions.

## Transition notifications

```sh
gregale webhooks add --app my-api \
  --target-url https://ops.example.com/gregale/production-routes \
  --secret "$WEBHOOK_SECRET" \
  --event routes.monitor.violated --event routes.monitor.escalated \
  --event routes.monitor.recovered
```

Events commit with saved incident state and app-only recipient snapshots. They
contain version, app/deployment/incident IDs, revision, status, checked time and an
authenticated incident path. When customer grouping is enabled, aggregate
observed and violated customer counts are included; identity UUIDs and request
data remain excluded. Existing signing, retries and delivery replay apply.
`routes.monitor.escalated` fires during an open incident when one or more route
error or latency signals newly become violated. It reports the number of newly
affected routes and signals, the previous evaluation time, and aggregate impact
counts from the current evaluation. Repeated violations of an already-violated
signal, unknown results, and recovery do not emit escalation events. Each
transition has a stable `transition_id`; event recipients can fetch the saved
incident from `incident_path` for route-level evidence. Late subscriptions
receive future transitions only. Context changes do not emit recovery. A failed
evaluation transaction leaves incident, timeline and notification state
unchanged and retries later.

The API and Go, Node and Python SDKs expose:

- `GET /v1/apps/{slug}/route-monitor`
- `PUT /v1/apps/{slug}/route-monitor`
- `GET /v1/apps/{slug}/route-monitor/report`
- `GET /v1/apps/{slug}/route-monitor/incidents`
- `GET /v1/apps/{slug}/route-monitor/incidents/{incident}`

To disable, save the current selectors with `--mode disabled` and the current
revision. An explicit empty JSON array can remove selectors in disabled mode.
