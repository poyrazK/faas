# Observed customer impact for route changes

`gregale preview report` connects changed routes to customers observed using
the selected parent deployment. A contract break now has context for client
migration and rollout planning: how many recorded consumers or tenants used
that route, their request volume, and when usage was last observed.

```sh
gregale preview report pr-42-api --since 168h --format markdown
gregale preview report pr-42-api --since 168h --customer-details --json
```

Reports use JSON version 5, including when source impact is supplied. The
`customers` envelope records availability, the baseline deployment, effective
window, `observed_only` coverage, retention clamping, route truncation and
whether identity details were requested. Each captured operation receives
`customer_impact` when bound evidence is available. Review priorities retain
this context and suggest checking observed customers for routes needing review.

Counts are included by default. `--customer-details` includes the top recorded
consumer/tenant identity groups, which can be resolved through the existing
consumer and platform-tenant APIs. Treat reports with that flag as customer
identity data. Neither mode exposes customer names, external references,
credential material, raw requests, query strings or payloads.

## Identity and deployment attribution

Only retained telemetry for the selected baseline deployment is read. An
explicit `--baseline-deployment` keeps the same parent ownership check as the
contract comparison. Candidate and other deployments never contribute to the
customer exposure counts. The request window is half-open `[from, until)`;
`until` is fixed at report generation time and the start is clamped to current
plan retention. Observation timestamps can represent collapsed minute buckets.

Consumer IDs identify stable authenticated app consumers, including historical
usage by revoked consumers. Platform tenant IDs come from the identity recorded
when the request occurred. Relinking a consumer today does not assign its older
traffic to the new tenant, and older rows without a tenant remain unassigned.
Consumer IDs are resolved only within the selected app/account, and tenant IDs
only within the account. Unresolvable identities are omitted from the detail
list. The current consumer-to-tenant link is never used to fill missing identity.

Consumer counts and tenant counts are separate, overlapping populations. One
tenant can have several consumers, and a consumer can appear with different
historical tenant identities. Do not sum them into a unique customer total.

`requests` includes the stored collapsed row weight. Its three disjoint parts are:

- `identified_requests`: at least one recorded identity resolves within scope.
- `anonymous_requests`: neither consumer nor tenant identity was recorded.
- `unresolved_identity_requests`: identities were recorded, but none resolve
  within the selected app/account boundary.

`customers` groups identified requests by the recorded consumer/tenant pair;
either ID can be absent. One resolvable identity is enough to identify a group.
The aggregate distinct counts include all retained observations before detail
caps; they do not depend on how many identity groups are returned.

## Bounds and missing evidence

The API returns the top 200 route/method rows by weighted request count and the
top 20 identity groups per route. Equal counts use deterministic route/method
and identity ordering. `routes_truncated` marks omitted routes. Per-route
`customers_truncated` and `other_customer_requests` disclose omitted identity
groups without reducing aggregate consumer/tenant counts.

The CLI joins exact captured method/path labels, accepting a recorded method
prefix. It never expands concrete paths into templates or infers renamed
parameters. Duplicate equivalent labels remain `ambiguous_route`. A route with
no matching row is `no_observations`, or `not_in_bounded_inventory` when the
route cap was hit. Neither means the route is unused. Current policy-only
routes do not receive captured-contract customer evidence.

Coverage is always `observed_only`. Sampling, dropped events, disabled recording,
expiry, and missing identity have no trustworthy denominator in this store.
These counts describe possible exposure to a changed route, not which request
fields a customer uses or whether their client will break. Customer evidence
is advisory and does not alter existing contract, requirements or test gates.

## API and SDKs

```http
GET /v1/apps/{slug}/analytics/route-customers?deployment_id={uuid}&since=7d
```

The endpoint uses normal read scopes and the existing paid request telemetry
entitlement. A foreign or missing app/deployment returns 404; malformed
deployment IDs and invalid windows return validation errors. Historical `until`
must remain inside the current retention window, rather than shifting retention
back to that date. Reads return no mutations or notifications.

Go: `client.GetAppRouteCustomerUsage(ctx, slug, faas.RouteCustomerUsageOptions{DeploymentID: id, Since: "7d"})`.
Node: `AppsService.getAppRouteCustomerUsage({slug, deploymentId: id, since: "7d"})`.
Python: `get_app_route_customer_usage.sync(slug, client=client, deployment_id=id, since="7d")`.

This is retained debugger telemetry. It is separate from the durable consumer
usage ledger and must not be used as a billing total or full customer inventory.
