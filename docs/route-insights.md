# Route insights

Every six hours Gregale looks at apps that ran in the last day and records
what it noticed about their routes, so you don't have to run a report to find
out:

| Kind | Recorded when | What to do |
| --- | --- | --- |
| `kept_awake` | A client calls a route at a steady interval and keeps your app awake at least half the day; without that route the app would park. | Cache the route at the edge, or call it less often than the idle timeout. See [What keeps your app awake](route-awake.md). |
| `cacheable` | The gateways measured that a route returns the same body to repeat requests, so an edge cache rule would rarely serve a stale answer. | Draft the rule with `gregale routes advise APP --cache-max-age N`. See [Route advisor](route-advisor.md). |
| `cache_stale` | A route already has a cache rule, and more than 5% of the times the edge refilled its copy the body had changed before the rule's max-age ran out: callers got stale responses. | Shorten the rule's max-age (the message names the measured safe one when there is one), or purge the cache when the content changes. |
| `cache_too_short` | A route's cache rule refills far more often than needed: the body stayed the same for at least twice the rule's max-age, and no refill found it changed. | Raise the max-age to the measured value; every refill it saves is a request that can wake the app. |

```sh
gregale routes insights my-api
gregale routes insights my-api --json
```

An insight stays active while evaluations keep observing it and resolves
after a day without being seen. The API is
`GET /v1/apps/{slug}/routes/insights`; it lists active insights, then those
resolved in the last seven days.

## Getting told

Subscribe an app webhook to `routes.insight.detected` and
`routes.insight.resolved` (or to all events). Each transition is delivered
once per subscription, signed and retried like other app webhooks. The
payload carries the kind, method, route, message, the measured evidence and
the detection times.

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
