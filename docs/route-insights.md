# Route insights

Every six hours Gregale looks at apps that ran in the last day and records
what it noticed about their routes, so you don't have to run a report to find
out:

| Kind | Recorded when | What to do |
| --- | --- | --- |
| `kept_awake` | A client calls a route at a steady interval and keeps your app awake at least half the day; without that route the app would park. | Cache the route at the edge, or call it less often than the idle timeout. See [What keeps your app awake](route-awake.md). |
| `cacheable` | The gateways measured that a route returns the same body to repeat requests, so an edge cache rule would rarely serve a stale answer. | Draft the rule with `gregale routes advise APP --cache-max-age N`. See [Route advisor](route-advisor.md). |

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
