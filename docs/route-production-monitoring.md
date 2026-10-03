# Monitor production route budgets

Production route monitoring continues after a release is promoted. It checks
absolute error and latency budgets against stored observations on the sole fully
serving production deployment. Monitoring is advisory and independent of the
canary guard and saved contract/policy checks.

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
Select at most 20 distinct labels, with no wildcards, expanded URLs or queries.

```sh
gregale routes monitor set my-api --mode enabled \
  --routes production-routes.json --expected-revision 0
gregale routes monitor get my-api --json
gregale routes monitor report my-api
gregale routes monitor report my-api --fail-on-unhealthy --json
```

Updates replace intent and require the current revision. Identical intent is a
no-op. Enabling requires request telemetry entitlement; disabling remains
available after a downgrade. The CLI rejects misspelled budget fields. Reports
are read-only and do not create incidents. The CI flag requires healthy evidence;
unknown, violated and disabled reports exit nonzero with the report printed.

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
```

The worker opens one aggregate incident when a budget violation is confirmed.
Repeated violations stay quiet. The opening report fixes deployment/commit,
configuration revision, budgets, anchors, counts and exact windows. It preserves
at most three violated route/signal diagnostic entries in selector order, errors
before latency, and explicitly reports omitted entries. This snapshot describes
the opening evaluation; later failures are not added to it.

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
windows on the same deployment, revision and anchors. The recovery report is
appended while opening evidence stays fixed. Unknown data never closes an
incident. Intent edits/disable and a new fully serving deployment mark the old
incident `superseded`, without claiming health recovery. During an ambiguous
split the previous incident remains open. Recurrence after recovery creates a new
incident. History retains the active incident plus up to 100 newest closed
incidents within 8 MiB; entries are capped at 512 KiB. Pruned IDs/cursors return
not found.

## Transition notifications

```sh
gregale webhooks add --app my-api \
  --target-url https://ops.example.com/gregale/production-routes \
  --secret "$WEBHOOK_SECRET" \
  --event routes.monitor.violated --event routes.monitor.recovered
```

Events commit with saved incident state and app-only recipient snapshots. They
contain version, app/deployment/incident IDs, revision, status, checked time and an
authenticated incident path. Route inventory, counts, customer identities and
request data are excluded. Existing signing, retries and delivery replay apply.
Late subscriptions receive future transitions only. Context changes do not emit
recovery. A failed evaluation transaction leaves incident and notification state
unchanged and retries later.

The API and Go, Node and Python SDKs expose:

- `GET /v1/apps/{slug}/route-monitor`
- `PUT /v1/apps/{slug}/route-monitor`
- `GET /v1/apps/{slug}/route-monitor/report`
- `GET /v1/apps/{slug}/route-monitor/incidents`
- `GET /v1/apps/{slug}/route-monitor/incidents/{incident}`

To disable, save the current selectors with `--mode disabled` and the current
revision. An explicit empty JSON array can remove selectors in disabled mode.
