# What keeps your app awake

A Gregale app parks when nothing calls it for its idle timeout, and you pay
only while it is awake (plan RAM + 8 MB per running second). One client that
calls it more often than the idle timeout keeps it awake all day.
`gregale routes awake` shows which routes keep your app awake, what each one
costs in awake time, and what would change without it:

```sh
gregale routes awake my-api
gregale routes awake my-api --since 24h --json
```

```text
my-api was awake 96% of the last 168h 0m: 161h 2m running, 41.5 GB-h, 9 wakes. Idle timeout: 60s.

ROUTE            REQUESTS  WAKES  AWAKE     SHARE  PATTERN      WITHOUT IT
GET /health         10079      1  152h 40m  95%    every ~60s   -149h 12m awake, +212 wakes
POST /orders          412      8  6h 3m     4%     irregular    no change
Unattributed                      2h 19m    1%     probes, prewarm, min instances, cron, queue

Suggestions
  GET /health is called about every 1m0s by anonymous clients and keeps my-api awake. Serve it from the edge cache (`gregale routes advise my-api` drafts the rule): a cached response never wakes the app.
```

The report only reads. The API is `GET /v1/apps/{slug}/routes/awake`. It needs
a plan with request telemetry and covers at most the plan's telemetry
retention (default window 7 days).

## How it is counted

- **Awake time** is your billed running time, minute by minute, summed over
  instances.
- **Requests that keep the app awake** are the ones that restart the idle
  timer: a successful (2xx) response from your app, or a request that woke it.
  Errors don't restart the timer, and responses the edge served itself (cache
  hits, edge answers, rejected credentials) never reach the app.
- Each awake minute is credited to the requests of the latest minute that
  still keeps the app awake. When several routes were called in that minute,
  the minute is split by request count.
- **Without it** recomputes your awake time as if that route's requests had
  never arrived, with all other requests unchanged. `+N wakes` means your
  other routes would find the app parked N more times; each of those requests
  waits for a wake.
- **Unattributed** time is awake time no recorded request explains:
  [synthetic probes](route-health.md), scheduled prewarm, minimum instances,
  queue, cron and `waitUntil` work.

Figures are estimates at minute resolution. The report covers your ten
busiest routes; the rest appear as "Other routes".

## Suggestions

A suggestion appears for a route called at a steady interval whose removal
would save at least 10% of your app's awake time.

| Kind | Suggested when | What to do |
| --- | --- | --- |
| `edge_cache` | Almost all requests are anonymous `GET` or `HEAD` requests, such as an uptime monitor or a public status endpoint. | Cache the route at the edge. `gregale routes advise` drafts the rule; a cached response is served without waking the app, and your monitor still sees the edge answer. |
| `call_less_often` | A client, usually a poller with credentials, calls the route more often than your idle timeout. | Call it less often than the idle timeout, or push changes to the client instead of being polled. |

See also: [Route advisor](route-advisor.md) for the rules themselves.
