# Customer health during a canary

A route's aggregate traffic can look healthy while a particular tenant or API
consumer experiences errors or slow responses. Optional customer health reports
compare those identities on the same selected routes, candidate/stable deployment
pair, and two closed observation windows as [route health](route-health.md).

```sh
gregale routes health report my-api --deployment CANDIDATE_UUID --customers
gregale routes health report my-api --deployment CANDIDATE_UUID \
  --customers --customer-group-by consumer --customer-details --json
```

Configure exact normalized routes with `routes health set` first. The default
customer dimension is `tenant`, grouping requests by the platform tenant UUID
recorded at request time across all its consumers. `consumer` instead groups
requests by the recorded API consumer UUID, independently of tenant membership.
Current consumer-to-tenant links never rewrite historical attribution. Revoked
consumers still contribute retained observations. These dimensions describe
identities; they do not establish a total number of unique end users.

Customer IDs are omitted unless `--customer-details` is supplied. Without IDs,
rows are labeled by their position for that report only; positions are not stable
customer identifiers. Names, external references and credentials are excluded.
`--customer-group-by` and `--customer-details` require `--customers`. The API uses
`customers=true`, `customer_group_by=tenant|consumer`, and
`customer_details=true` on the existing live route-health report endpoint. Go's
`GetRouteHealthReportWithOptions` and the generated Node/Python endpoint options
expose the same behavior. Existing requests retain their original response.

Each customer's error comparison requires at least 20 represented requests on
**each deployment, in each window**. Selected latency checks require 100 per
side/window. The existing 5xx thresholds, latency budget and relative slowdown
checks are applied independently per identity. A confirmed regression requires
two consecutive windows for the same signal. A cohort seen on just one deployment
is included with unknown evidence, rather than compared against another customer.
Sparse, mixed, missing or pre-anchor evidence is also unknown.

Reports include weighted requests, 5xx counts/rates, optional weighted p95 estimates,
window reasons, and candidate/stable attribution totals. For the selected
identity dimension, requests without a recorded identity are `unattributed`;
recorded identities that cannot resolve within the owning account/app are
`unresolved`. Neither category becomes a customer cohort. These counts are
shown separately and prevent a healthy customer summary. Counts reconcile with
the aggregate report in the same repeatable-read database snapshot.

Each route returns at most 20 customer cohorts from the union of both deployments.
Ranking favors candidate 5xx counts, then combined request volume, then UUID order.
The report includes the full observed cohort count, an explicit truncation flag,
and candidate/stable request counts outside the output cap. A confirmed returned
regression takes precedence in the advisory summary. Otherwise empty, unknown,
unattributed, unresolved or truncated evidence makes the summary unknown. A
healthy summary means the retained observed comparisons passed; coverage remains
`observed_only`, with no claim of complete capture, customer coverage or a
statistical SLO. Publisher aggregation weights are preserved, and p95 uses bucket
representatives as described in the route-health guide.

The report is advisory. Customer findings do not change aggregate health, the
`--fail-on-unhealthy` exit result, canary gates, automatic recovery, saved decision
history, traffic audits or webhooks. Reads do not write rollout state. Customer
reports require the existing app-read authorization, completed MFA and available
request telemetry entitlement. Unavailable observations stay explicit. Use
[preview customer impact](route-customer-impact.md) to assess baseline exposure
before a release; use these reports to investigate observed canary behavior.
