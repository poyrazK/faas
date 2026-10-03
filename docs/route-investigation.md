# Investigate a route health signal

`gregale routes health investigate` selects bounded request examples from the
exact route, candidate/stable pair and closed windows used by live route health.
It connects a rate comparison to the existing production debugger:

```sh
gregale routes health investigate my-api --deployment CANDIDATE_UUID \
  --route "POST /checkout" --status 403
gregale routes health investigate my-api --deployment CANDIDATE_UUID \
  --route "POST /checkout" --status 403 \
  --customer-id TENANT_UUID --out investigation.json --json
gregale routes health investigate my-api --deployment CANDIDATE_UUID \
  --route "POST /checkout" --customer-group-by consumer --customer-id CONSUMER_UUID
```

The method/path must be an exact configured [route-health](route-health.md)
selector. Expanded URLs and arbitrary OpenAPI parameter names do not match.
Omitting `--status`, or setting it to zero, selects all 500–599 responses. A
nonzero status must be 401, 403, 404, 422 or 429 and must appear in that selector's
[watch_statuses](route-client-errors.md). Individual 5xx codes and latency-only
investigations are outside this first version.

## Comparison and customer scope

The response includes the full aggregate health report, the selected route
finding, the selected signal's independent status/reason and diagnostic windows.
The aggregate report keeps its rollout meaning. For example, aggregate health
can be healthy while a selected customer's 403 signal is regressed.

`--customer-id` selects one owned recorded identity; the default dimension is
`tenant`. `--customer-group-by consumer` selects an app API consumer independently
of tenant membership. These flags deliberately include the supplied UUID in the
report. Other customer UUIDs, names, external references and credentials are
excluded. Selection uses request-time attribution, never the consumer's current
tenant link. Revoked consumers remain investigable while their telemetry is
retained. A requested customer can be outside the usual top-20 customer report;
its counts and optional latency evidence are computed directly for this scope.
A customer with no retained observations stays unknown.

The aggregate report, targeted finding and example inventory share one read-only
repeatable-read snapshot. The two closed windows, ingestion allowance,
configuration revision, stage anchor, deployment IDs and commit SHAs are returned
with the report. Editing configuration still resets the shared anchor as described
in route health. Pre-anchor examples may be present, but their comparison remains
unknown. Each invocation captures the current live comparison; it does not reopen
a saved historical decision.

## Examples and inspection

Each deployment/window returns at most **three** matching telemetry rows, at most
12 across the investigation. Matching represented-request totals and retained-row
counts are computed before this cap. `examples_truncated` explicitly reports
omission. Trace-linked rows come first, then the newest timestamp and descending
telemetry UUID. Missing stable comparisons return unavailable evidence with zero
rows. A healthy stable comparison with no matching rejections has zero examples.

Examples include the telemetry-row ID, timestamp, status, latency bucket,
represented-request count, optional trace ID and authenticated debugger evidence
path. Human output supplies commands such as:

```sh
gregale debug requests inspect my-api TELEMETRY_ROW_UUID
gregale debug requests trace my-api TELEMETRY_ROW_UUID
```

Inspection reuses retained, redacted request evidence, dependency latency and
trace views. A collapsed row can represent multiple requests: its trace link does
not establish a separate trace for every represented request. A link can exist
while span evidence is missing or has expired. Following it rechecks debugger
authorization, MFA, plan entitlement and current retention. The investigation
exports references and metadata; it does not embed span/log contents or capture
request bodies, headers or raw URLs.

Coverage remains `observed_only`; neither the examples nor stored weights establish
complete capture, a total end-user count or a cause for a regression. Reads never
advance, abort, replay requests or save rollout decision history. The command
returns zero for a successfully validated investigation, including unknown or
regressed signals; this is a diagnostic command.

`--out` creates a new owner-only JSON file and refuses existing files or symlinks.
The CLI validates response scope, selected findings, windows, matching counts,
example bounds and evidence paths before printing or exporting. Go, Node and
Python SDKs expose the typed endpoint:
`GET /v1/apps/{slug}/route-health/deployments/{deployment}/investigation` with
`method`, `path`, `status_code`, optional `customer_id` and `customer_group_by`.
It requires app-read access, completed MFA and request telemetry entitlement.
