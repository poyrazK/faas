# ADR-20261026143210: Route labels go to routes the app serves

- **Status:** accepted
- **Date:** 2026-10-26
- **Related:** ADR-093 (per-route metrics), ADR-20261024154324 (routes at
  public scale), ADR-1188 (learned cacheability), ADR-498 (production route
  monitoring)
- **Amends:** ADR-093 D2 (admission order)

## Context

A review of the routes code after ADR-20261024154324 found three more gaps.

1. **Anyone can use up an app's route labels.** Each gatewayd-internal gives an
   app at most 50 route labels and never takes one back until it restarts
   (ADR-093 D2). It assigned a label before forwarding the request, whatever
   the app answered. Internet scanners probe every public hostname with paths
   the app does not serve (`/wp-login.php`, `/.env`), and any client can send
   made-up methods. After 50 such requests the app's real routes were all
   `__route_other__` on that gateway: request telemetry, the production route
   monitor (which then saw no requests on its budgeted routes and stayed
   unknown), insights, the awake report, canary route health and the route
   list all lost them. Scanned labels also took slots in the gateway-wide
   series budget.
2. **Analytics reads had no time bound.** Route advice, the awake report and
   the insights evaluator aggregate the whole requested window of retained
   request telemetry, up to 7 days at 10,000 rows a minute on Pro and 14 days
   at 50,000 on Scale, with no statement timeout.
3. **Some routes endpoints ignored the plan.** The capability catalog lists
   route priorities, throttle suggestions and route lifecycle review for
   Hobby and above, but nothing enforced it.

## Decision

**Admission after the response.** A request whose route has no label yet
carries it as pending; Handler.observe decides from the response. The route
is admitted when the app itself answered (the first-byte recorder is stamped)
with a status that shows the route exists: any 2xx, 3xx or 5xx except 501,
or a 4xx other than 400, 401, 403, 404, 405, 410, 414 and 431. Otherwise that
request is recorded as `__route_other__`. A route that matches a declared
template (OpenAPI or gregale.yaml) is admitted on sight. Only GET, HEAD, POST,
PUT, PATCH, DELETE and OPTIONS carry a route label; any other method is
`__route_other__`. The first request of a real route is labelled by its own
response, so nothing is lost. Labels stay non-evicting (ADR-093 D2).

**Bounded analytics reads.** Every route advice and awake read runs in a
read-only transaction with `statement_timeout` set to
`RouteAnalyticsQueryTimeout` (10 s). A cancelled read returns 503 telling the
caller to retry with a shorter `?since`; the insights evaluator counts that
app as failed and moves on.

**Plan checks.** Route priorities, throttle suggestions, route lifecycle
history and approvals, and setting the route removal policy return 402 on
the Free plan. Route removal checks and policy reads stay open, because
traffic promotion consults them.

## Consequences

- Scanner 404s and made-up methods no longer touch an app's labels or the
  gateway's series budget, so the monitor, insights and route list keep the
  app's real routes.
- Routes the gateway answers without the app (edge redirects, cache hits on a
  gateway that has not yet seen a miss) are `__route_other__` until the app
  answers them once.
- A route that only ever answers 401 or 403 earns its label when an
  authorized request succeeds.
- An app that answers every path with 200 (a single-page app's catch-all) can
  still have its labels filled by scanners; declaring its routes in OpenAPI
  or gregale.yaml keeps them labelled.
- Billing route labels for attributed consumer and tenant traffic are
  unchanged: that traffic carries credentials.

## Amendment 1 (2026-10-27) · route evidence survives catch-all apps and busy siblings

Two gaps from the consequences above stayed open after the first change.

**A catch-all app could still fill its labels.** An app that answers every
path with 200 establishes every probed path, so scanners filled its 50 labels
and every real route after them stayed `__route_other__` until the gateway
restarted. A full set now gives a new route the slot of its least recently
seen label, but only once that label has gone `RouteLabelReuseAfterIdle`
(1 h, pkg/api/limits.go) without a request on that gateway. A declared route
is never replaced, and a label in use is never displaced. The replaced
label's per-route series are deleted with it, so live series stay within the
app's cap and the gateway's series budget (ADR-20261024154324); churn is at
most one replacement per label per idle window. This replaces "labels stay
non-evicting" above and ADR-093 D2's lifetime-of-the-daemon rule.

**One busy app could starve its siblings' telemetry.** apid admits request
telemetry rows against one token bucket per account
(`DebugTelemetryRequestsPerMinute`). A busy app could take every token, so
its sibling apps' rows were refused and their route monitor, canary guard and
insights went blind. The bucket now also tracks each app's rows per minute.
While the bucket is below `DebugTelemetryFairSharePressure` (half) of its
capacity, an app that has already used its fair share of the minute
(capacity divided by the apps active this minute or the last) yields; apps
below their share keep the remaining tokens. The account total is unchanged,
and an account with one active app is limited only by its bucket. A refused
row is `RATE_LIMITED` as before.
