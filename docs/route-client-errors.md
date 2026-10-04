# Watched response codes during a canary

Use [route investigation](route-investigation.md) to connect a watched-code
finding to retained candidate/stable request examples and debugger inspection.

A deployment can break a customer's authorization, input validation, route lookup
or rate-limit behavior while its aggregate 5xx and latency checks remain healthy.
Gregale can compare selected 4xx response rates against stable on critical routes
and, optionally, for individual tenants or API consumers.

Add `watch_statuses` to an existing [route-health selector](route-health.md):

```json
[
  {"method": "POST", "path": "/checkout", "watch_statuses": [403, 422]},
  {"method": "POST", "path": "/login", "watch_statuses": [401, 429]},
  {"method": "GET", "path": "/profiles/{id}", "watch_statuses": [404]}
]
```

Select distinct codes from **401, 403, 404, 422 and 429**, up to five per route.
Omitting the field or supplying an empty array disables these comparisons.
Choose codes whose increase would indicate a behavior change for that route;
expected rejections already present on stable do not by themselves regress.
Method and gateway-normalized path must match exactly.

```sh
gregale routes health get my-api --json
gregale routes health set my-api --routes critical-routes.json \
  --mode report --expected-revision CURRENT_REVISION
gregale routes health report my-api --deployment CANDIDATE_UUID
gregale routes health report my-api --deployment CANDIDATE_UUID \
  --customers --customer-group-by tenant --json
```

Use the revision and mode appropriate to the existing configuration. This field
belongs to the same revisioned configuration as route and latency selectors.
Changing only watched codes increments the revision and resets the shared
observation anchor, so **all selected health checks require fresh windows** after
the edit. An enforced rollout can therefore wait for new observations following
a configuration edit, even though the 4xx findings themselves are advisory.

## Evidence and thresholds

Every selected code is compared independently using the same two closed UTC
minute windows, ingestion allowance, stage/configuration anchor and immutable
candidate/stable deployment pair as route health. Counts preserve telemetry
publisher weights. Rates divide responses with that exact code by all represented
requests for the route, deployment and window, or for that customer cohort.

Each deployment must have at least **20 represented requests per window**. A code
regresses in a window when the candidate has all of:

- At least two responses with that code.
- A rate of at least 5%.
- A rate at least three times stable's rate.
- A rate at least five percentage points above stable's rate.

The **same code must regress in both windows** to confirm a regression. A 403 rise
in one window followed by a 422 rise in the other remains unknown. Sparse,
missing, pre-anchor or mixed evidence also remains unknown. Two healthy windows
produce a healthy comparison, including sustained rejections with similar rates
on both deployments. These are observed comparisons, not a statistical SLO.

Only telemetry attributed to the compared deployments contributes. Gateway
rejections before a deployment is selected cannot be compared to candidate or
stable and are excluded. Coverage remains `observed_only`; publisher loss,
sampling and retention can leave gaps that Gregale cannot measure.

## Reports and customer attribution

Live reports add `client_error_status` and `client_error_reason`. Each watched
route includes `watch_statuses` and `client_errors`, containing thresholds and
per-code verdicts, reasons and window counts/rates. The report's existing
`status`, route `status`, and 5xx/latency evidence keep their existing meaning.
The CLI renders watched codes as advisory signals. Go, Node and Python SDKs
expose the new typed configuration and report fields.

With `--customers`, each cohort's `health.client_errors` contains the same
per-code evidence for its recorded tenant or consumer. The advisory customer
summary includes watched codes alongside existing 5xx and latency signals.
Request-time identity attribution, unresolved/unattributed counts, ID opt-in and
the output cap follow [customer health](route-customer-health.md). Ranking favors
candidate 5xx responses, then responses with selected watched codes, then combined
request volume and UUID order. Counts and omitted volume precede the output cap;
the returned cohorts do not establish complete customer coverage.

Aggregate, code and requested customer evidence are read from one repeatable-read
snapshot. Reads do not write rollout state. These findings do not change
`--fail-on-unhealthy`, canary advancement, automatic recovery, traffic audits or
webhooks. Saved decisions may retain the configured watched-code selectors, but
do not store live 4xx evidence or customer identities. Use live reports to
investigate these signals.
