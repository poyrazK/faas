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

ROUTE                     REQUESTS  WAKES  AWAKE     SHARE  PATTERN                        WITHOUT IT
GET /health               10079     1      148h 10m  92%    every ~60s                     -141h 2m awake, +212 wakes
POST /orders              412       8      6h 3m     4%     irregular                      no change
Cron /sync (*/5 * * * *)  2016             4h 30m    3%     every ~300s
Queue emails              388              1h 10m    1%
Scheduled prewarm         3                1h 30m    1%
Unattributed                               49m       0%     min instances, pruned history

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
- Minutes no request explains are credited to the other things that keep
  the app awake:
  - **Background work** Gregale delivered to the app: cron jobs (one row per
    job, named by the path or command it runs and its schedule), queues (one
    row per queue), delayed tasks, async invokes, inbound webhooks, replays
    and event sources. A delivery counts from when it reached the app until it
    completed, at most 60 minutes, and keeps the app awake for the idle
    timeout after that, like a request.
  - **Route probes** your route health selectors send during a canary
    ([synthetic probes](route-health.md); kept for a day).
  - **Scheduled prewarm**.

  Customer requests always come first.
- **`waitUntil` work** belongs to the request that registered it. An instance
  can't park while a `waitUntil` task is still running, so a request keeps
  the app awake for the longer of the idle timeout and your plan's `waitUntil`
  timeout (5, 15, 30 or 60 seconds). The JSON reports it as
  `wait_until_timeout_seconds`.
- **Without it** recomputes your awake time as if that route's requests had
  never arrived, with all other requests, background work, probes and
  prewarms unchanged. `+N wakes` means your other routes would find the app
  parked N more times; each of those requests waits for a wake.
- **Unattributed** time is awake time nothing recorded explains: minimum
  instances, or history Gregale no longer keeps (probes older than a day,
  deliveries whose results retention already removed). With minimum
  instances set the app never parks, and the report says so.

Figures are estimates at minute resolution. The report covers your ten
busiest routes; the rest appear as "Other routes".

## Suggestions

A suggestion appears for a route called at a steady interval whose removal
would save at least 10% of your app's awake time, and for a cron job that
fires more often than the idle timeout.

| Kind | Suggested when | What to do |
| --- | --- | --- |
| `edge_cache` | Almost all requests are anonymous `GET` or `HEAD` requests, such as an uptime monitor or a public status endpoint. | Cache the route at the edge. `gregale routes advise` drafts the rule; a cached response is served without waking the app, and your monitor still sees the edge answer. |
| `call_less_often` | A client, usually a poller with credentials, calls the route more often than your idle timeout. | Call it less often than the idle timeout, or push changes to the client instead of being polled. |
| `run_less_often` | A cron job fires at a steady interval shorter than your idle timeout and keeps the app awake for at least 10% of its awake time. | Run the job less often than the idle timeout. The app then parks between runs, and each run wakes it. |

See also: [Route advisor](route-advisor.md) for the rules themselves.

Routes that keep your app awake are also recorded every six hours as
[route insights](route-insights.md), which can notify a webhook.
