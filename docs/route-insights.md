# Route insights

Every six hours Gregale looks at apps that ran in the last day and records
what it noticed about their routes, so you don't have to run a report to find
out. Most insights come with a fix you can apply in one step:

| Kind | Recorded when | Fix |
| --- | --- | --- |
| `kept_awake` | A client calls a route at a steady interval and keeps your app awake at least half the day; without that route the app would park. | When the route is also `cacheable`, the same cache rule. Otherwise only the caller can change: call it less often than the idle timeout. See [What keeps your app awake](route-awake.md). |
| `cacheable` | The gateways measured that a route returns the same body to repeat requests, so an edge cache rule would rarely serve a stale answer. | Create an enabled cache rule for the route's GET and HEAD requests on every app host, with the measured safe max-age. |
| `cache_stale` | A route already has a cache rule, and more than 5% of the times the edge refilled its copy the body had changed before the rule's max-age ran out: callers got stale responses. | Lower the rule's max-age to the measured safe one, or disable the rule when no max-age was safe, then purge the route's cached responses. |
| `cache_too_short` | A route's cache rule refills far more often than needed: the body stayed the same for at least twice the rule's max-age, and no refill found it changed. | Raise the rule's max-age to the measured one; every refill it saves is a request that can wake the app. |

```sh
gregale routes insights my-api
gregale routes insights my-api --json
gregale routes insights my-api --fix INSIGHT_ID
```

The app's dashboard page lists the same insights under **Route insights**,
each with an **Apply fix** button.

An insight stays active while evaluations keep observing it and resolves
after a day without being seen. The API is
`GET /v1/apps/{slug}/routes/insights`; it lists active insights, then those
resolved in the last seven days.

## Applying a fix

Each active insight with a fix carries a `fix` object: the action, a summary,
the max-age it sets, the rule it changes and whether it purges the cache. The
fix is planned when you read the insights, from the insight's evidence and
your current edge rules, so it describes exactly what applying it would do now.

`POST /v1/apps/{slug}/routes/insights/{id}/fix` with `{"fix_id": "..."}`
applies it. If the insight or your edge rules changed after you read the fix,
the request fails with `409 route_insight_fix_changed` and nothing changes:
read the insights again and review the new fix. The CLI and the dashboard do
this for you.

A fix never widens what a rule matches:

- A cache rule that serves exactly this route is edited in place.
- A rule that also covers other routes, or that comes from the app's manifest,
  is left alone. The fix adds a narrower rule for this route ahead of it. The
  new rule copies the original's host, headers, conditions and cache settings
  and sets only the max-age.
- When no max-age was measured safe and the rule covers other routes, there is
  no automatic fix; change the rule on the edge-rules page.

Fixes go through the same path as the edge-rules API, so plan quotas and
validation apply, and the result is an ordinary edge rule you can edit,
disable or delete. Rules created by a fix are enabled; `gregale routes advise
--apply` still creates rules disabled for review.

Applying a fix resolves the insight; its `fix_action` and `fixed_at` record
what resolved it. Evidence lags a change by up to a day, so a fixed insight is
not reopened until a full evaluation window has passed since the fix.

## Getting told

On the dashboard, enable the **New route insights** alert preset and bind an
email, Slack or PagerDuty channel. The alert fires when insights became active
in its window (24 hours by default) and lists up to five of them in the
notification. The metric is `route_insights_detected`; it takes window 6h, 24h
or 7d.

Or subscribe an app webhook to `routes.insight.detected` and
`routes.insight.resolved` (or to all events). Each transition is delivered
once per subscription, signed and retried like other app webhooks. The
payload carries the insight id, kind, method, route, message, the measured
evidence, the detection times and, for a fixed insight, the fix that resolved
it.

Insights need request telemetry, so they follow the same plan gate as the
route advisor; Free plans have none.

## How cache rules are checked

Each time a gateway stores a fresh copy for a cached route, it compares the
body with the one it stored before for the same URL. A body that changed
within the rule's max-age (plus a 10% lag, at least 5 seconds) counts as a
stale refill; an unchanged body extends how long the content is known to stay
the same. Only GET bodies are compared, never stored outside the gateway, and
each gateway compares only its own refills. A rule needs at least 20 refills
before it can be called stale.
