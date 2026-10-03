# Critical route health during canary rollouts

Gregale can compare observed 5xx rates and optional p95 latency checks on selected
critical routes between a canary and its serving stable deployment. A busy healthy
route cannot hide a failure on a selected checkout or login route. This operates independently of
saved route policy requirements and the Test CLI.
For monitoring after promotion, use [production route budgets](route-production-monitoring.md).

Create a JSON selector file using exact **gateway-normalized telemetry paths**.
Use the method/path labels in debugger analytics, rather than expanded request
URLs or arbitrary OpenAPI parameter names. For example, the gateway normalizes
`GET /profiles/238` to `GET /profiles/{id}`. Method and path must match exactly;
wildcards, queries and fragments are rejected. Select up to 20 routes.

```json
[
  {"method": "POST", "path": "/checkout"},
  {"method": "GET", "path": "/profiles/{id}"}
]
```

Start in report mode and inspect an existing canary:

```sh
gregale routes health set my-api --routes critical-routes.json --mode report --expected-revision 0
gregale routes health report my-api --deployment CANDIDATE_UUID
gregale routes health report my-api --deployment CANDIDATE_UUID --fail-on-unhealthy --json
```

The report includes candidate/stable deployment IDs and available commit SHAs,
configuration revision, current canary step, route verdicts, observation windows,
weighted request and 5xx counts, rates, and reasons. Selected latency checks also
include candidate/stable p95 estimates, delta, ratio, budget and independent
signal verdicts. The CI flag exits nonzero for regressed, unknown or disabled
reports. Report mode does not block progression.

After assessing traffic volume and telemetry availability, opt in using the
current revision:

```sh
gregale routes health get my-api --json
gregale routes health set my-api --routes critical-routes.json --mode enforce --expected-revision 1
```

Enforcement requires a plan with traffic splitting and request telemetry. It
pauses subsequent manual and automated canary advances unless every selected
route is healthy. Initial activation is unchanged. Legacy rollout advance/promote
cannot bypass enforcement. A blocked request returns `route_health_blocked` and
points to the report. Abort remains available. The existing aggregate circuit
breaker can still abort independently under its existing policy.

Each route is compared in two consecutive closed UTC minute windows, with a
30-second ingestion allowance. Both windows must begin after the current stage
and the latest selector, latency-check, watched-status or mode update. Changes therefore require
new observations.
At least 20 represented requests are required on each deployment **per route,
per window**. Counts preserve telemetry publisher aggregation weights.

A window regresses when the candidate has at least two 5xx responses, a rate
of at least 5%, at least three times stable's rate, and at least five percentage
points above stable. Two regressing windows confirm a regressed route. Two
healthy windows produce healthy evidence. Missing, sparse or mixed evidence is
unknown and holds an enforced rollout. The stable comparison must be the sole
other live serving deployment in the same scope.

Coverage is explicitly `observed_only`: the system cannot establish how many
requests were dropped before telemetry storage. The ingestion allowance helps
with normal publishing delay but does not guarantee completeness. This compares
stored observations; it is not a full capture guarantee or a statistical SLO.
Low-traffic routes may need more real requests before progression can resume.
Live reports move with their observation windows. Evaluated canary advances now
retain their exact decision evidence for later explanation.

## Advisory customer comparisons

Use [route investigation](route-investigation.md) to retrieve bounded matching
5xx or watched-code examples and open their existing debugger evidence.

Use `--customers` to compare tenants or API consumers within the same observation
windows. IDs require `--customer-details`. Customer evidence remains advisory;
see [customer health](route-customer-health.md) for samples, attribution and caps.

## Advisory 4xx comparisons

Add `watch_statuses` to a selector to compare selected 401, 403, 404, 422 or 429
response rates against stable. Reports expose separate aggregate and optional
customer evidence. These findings stay advisory and do not affect the report's
aggregate health verdict or rollout decisions. See
[watched response codes](route-client-errors.md) for configuration and thresholds.

## Optional latency checks

Add a p95 budget, a relative slowdown check, or both to individual selectors:

```json
[
  {"method": "POST", "path": "/checkout", "max_p95_ms": 500, "check_latency": true},
  {"method": "POST", "path": "/login", "max_p95_ms": 300},
  {"method": "GET", "path": "/profiles/{id}", "check_latency": true},
  {"method": "GET", "path": "/health"}
]
```

`max_p95_ms` is an integer from 0 to 86,400,000 milliseconds. A positive value
enables the absolute budget: candidate p95 strictly above it violates the budget,
even if stable is also slow. Equality is allowed. Omitted or zero disables the
budget. `check_latency: true` independently enables relative detection: candidate
p95 must be at least 1.5 times stable **and** at least 100 ms slower to regress.
A budget alone does not enable relative detection. A selector without either
option retains its original 5xx-only behavior.

Latency decisions require at least **100 represented requests on each deployment,
per selected route, per window**, using the same two windows and evidence anchors
as error checks. Missing percentiles, insufficient requests, or unavailable
telemetry produce unknown latency evidence. Zero is a valid measured percentile;
the ratio is omitted when stable p95 is zero. A confirmed 5xx regression still
takes precedence over unknown latency evidence.

Errors and latency are confirmed independently across both windows. A latency
violation in one window followed by an error regression in the other leaves the
route unknown; it does not confirm a sustained regression in either signal.
Two latency violations hold an enforced rollout, while report mode exposes the
same evidence without blocking progression. Editing only a budget or latency
flag increments the revision and requires fresh observations.

Percentiles are estimates weighted by collapsed telemetry request counts. Each
publisher bucket contributes its inclusive upper-bound latency with its request
weight; the original individual latency distribution cannot be recovered. Measurements
include all observed statuses and the gateway's end-to-end request latency,
including cold boot time. They retain the `observed_only` coverage limitation and
do not certify a latency SLO.

## Explain a saved rollout decision

```sh
gregale routes health explain my-api --deployment CANDIDATE_UUID
gregale routes health explain my-api --deployment CANDIDATE_UUID --decision DECISION_UUID --json
gregale routes health explain my-api --deployment CANDIDATE_UUID --limit 5 --before DECISION_UUID
```

The default command explains the newest saved health decision and shows a timeline
of up to five decisions. Use `--decision` for one snapshot, or `--limit` (1–10) and
`--before` to page through retained decisions. `--json` exports the saved evidence.
Advance responses and traffic audits include `route_health.history_id`; held
advance errors include an explain command with the saved decision ID.

Snapshots include candidate/stable IDs and available commits, stage, intent
revision, observation anchor, previous/requested traffic, manual/worker source,
counts, p95 estimates, verdicts and the exact evaluation thresholds. A health hold
saves evidence without changing traffic or its traffic audit. Allowed and report-
mode snapshots commit with a successful advance. Reads never create history.
Attempts blocked before the health guard runs, such as stale-step or configured-
policy conflicts, do not create a health decision. Default unconfigured routes
produce no history.

Identical retries with the same evidence, windows, source and requested transition
reuse the original snapshot. Changed observations or windows create a new snapshot.
History retains the newest 100 entries, up to 4 MiB per deployment and 64 KiB per
entry. Pruned IDs or cursors return not found. Existing history stays readable
after a plan downgrade while its app and deployment still exist.

The timeline describes evaluated advance attempts. A restored-health annotation
requires an allowed healthy decision after a hold with the same stage, revision,
stable identity and observation anchor. Changing configuration or stage is labeled
as a context change. The timeline cannot infer an incident start or recovery that
was not evaluated. Use `routes health report` for current observations; saved
decisions do not authorize a new advance.

## Automatic recovery on confirmed critical-route errors

The default `on_regression` action is `hold`. Opt into automatic recovery with:

```sh
gregale routes health set my-api --routes critical-routes.json \
  --mode enforce --on-regression abort --expected-revision CURRENT_REVISION
```

The existing canary worker asks APID to check current observations before the
stage timer and aggregate gates. Two confirmed 5xx regression windows on a
selected route can abort the candidate and restore its exact serving predecessor.
The predecessor must be unique, older and serving in the same environment.
Sparse, unavailable, mixed or latency-only evidence retains existing holds.
Report mode remains observational. Existing aggregate circuit breakers continue
independently under their own policy.

Changing the action increments the configuration revision and requires fresh
windows after the update. Every recovery evaluates current policy, stage and
telemetry under locks; an old saved decision cannot trigger rollback. Missing or
expired worker leases and failed persistence leave traffic unchanged.

Subscribe to `routes.health.aborted` using an app webhook. The event commits with
traffic restoration and includes the exact saved decision. `routes health explain`
shows `aborted`, worker source, `purpose: abort`, and requested candidate traffic
zero. The same bounded history and app read/MFA requirements apply. Healthy or
unknown periodic recovery checks do not produce saved history. Retries do not
reopen an aborted candidate or claim a healthy resume.

`on_regression` is replacement intent: omission, including in older SDK calls,
means hold. Use `--on-regression hold` with the current revision to turn off
route-specific automatic recovery while retaining the rest of the guard.

## Notifications for held and resumed releases

Subscribe through an app webhook:

```sh
gregale webhooks add --app my-api \
  --target-url https://ops.example.com/gregale/route-health \
  --secret "$WEBHOOK_SECRET" \
  --event routes.health.blocked \
  --event routes.health.resumed
```

`routes.health.blocked` fires when an enforced health guard first holds an advance.
`health_status` distinguishes `unknown` evidence from a confirmed `regressed`
result. A hold that changes from unknown to regressed emits one additional blocked
event. Repeated retries, moving observation windows and changed counts do not
repeat the same hold. Unknown data after a regression never clears it.

`routes.health.resumed` fires only when comparable healthy evidence permits a
successful traffic advance. Changing the stage, revision, observation anchor,
stable deployment or evaluation policy starts a new context. A fresh healthy
check, switching to report mode, removing selectors or aborting the canary never
claims a health recovery. These events describe evaluated rollout attempts.

Payloads contain `decision_id`, an authenticated `history_path`, status/reason,
deployment identities, stage/revision, source and previous/requested traffic.
Resume also identifies its comparable `blocked_decision_id`. Fetch the saved
snapshot with app read scope and completed MFA, or use:

```sh
gregale routes health explain my-api --deployment CANDIDATE_UUID --decision DECISION_UUID
```

Route inventory, counts, request data and secrets are excluded from webhook
payloads. Snapshot references remain subject to history retention; a prior hold
may already be pruned when its resume is delivered. Recipient snapshots and
notification state commit with the decision. A failed advance cannot leave a
resume event. Later subscriptions receive future transitions only, and these
events are available only to app receivers. Existing webhook signatures, retries
and delivery replay apply; see [receiver verification](webhook-receiver-verification.md).

To remove enforcement while preserving selectors, set report mode with the
current revision. To remove selectors, use an explicit empty JSON array in
report mode. This remains available after a plan downgrade.

The API exposes:

- `GET /v1/apps/{slug}/route-health/gate`
- `PUT /v1/apps/{slug}/route-health/gate` with `mode`, `expected_revision`, and `routes`
- `GET /v1/apps/{slug}/route-health/deployments/{deployment}`
- `GET /v1/apps/{slug}/route-health/deployments/{deployment}/history`
- `GET /v1/apps/{slug}/route-health/deployments/{deployment}/history/{decision_id}`

Reads require app read scope and completed MFA; writes require deployment write
scope and completed MFA. Evidence and configuration are scoped to the owning
account and app. Go, Node and Python SDKs expose these endpoints.
