# Check an app and explain a rollback

Two debugger commands answer the first questions in an incident: is the app
healthy, and if a release was rolled back or is stuck, why.

## Is my app OK?

```sh
gregale debug status my-api
gregale debug status my-api --since 24h
```

```
my-api: rolled back
  Rolled back at 10% traffic: 5xx regression (candidate 95.2%, stable 0.0%; 20/21 vs 0/21 requests). Worst route /checkout: 20 of 21 requests failed with a server error, vs 0 of 21 on the stable release.

Health (last 1h)  1240 requests, 23 server errors, p95 182 ms
Serving           3f2a9c41  100%  commit a1b2c3d4
Release           Rolled back at 10% traffic: 5xx regression ...
Regressions       /checkout  errors 95.2% (was 0.0%)  deployment 7d1e2f3a
Recent failures   12:04:31  GET /checkout  500  0f9c2e7a51b84d6e9a3f1c2b7d8e4a60
Tracing           on

Next:
  gregale debug rollout my-api
  gregale debug requests inspect my-api 0f9c2e7a51b84d6e9a3f1c2b7d8e4a60
  gregale debug regressions my-api
```

The first line is the verdict, worst first:

| Verdict | Meaning |
|---|---|
| `failing` | At least 5% of at least 20 requests ended in a server error |
| `rolled_back` | A canary release was rolled back inside the window |
| `degraded` | An instance ran out of memory, a regression is open, or some requests failed |
| `releasing` | A canary release is rolling out |
| `healthy` | Requests were served with no server errors |
| `idle` | No requests in the window |

The window defaults to the last hour and never reaches further back than your
plan's debugger retention. `--json` returns the full response from
`GET /v1/apps/{slug}/debug/status`.

When the kernel killed one of your app's processes for using more memory
than its plan allows, the status lists the kill with the app's peak memory
and what to change:

```
Out of memory     11:50:02  peak 600 MB, plan 512 MB  deployment 3f2a9c41  (instance replaced)

Memory:
  • upgrade from a 512 MB plan to a plan with at least 608 MB of RAM
  • trim in-memory state (caches, buffers, large request bodies held in memory)
```

A live release keeps serving after an out-of-memory kill: the instance is
replaced and the next request starts a new one. A deployment that is still
starting or rolling out fails instead (`app_runtime_oom`), and your previous
release keeps serving.

## Why was my release rolled back, or why is it waiting?

```sh
gregale debug rollout my-api
gregale debug rollout my-api --deployment-id 7d1e2f3a-...
```

The command explains your newest canary release, or the one you name.
`gregale debug explain` is the same command. It shows:

- the safe-release decision: rolled back, held, rolling out or complete, with
  the rule and the numbers the decision used;
- your routes on the release compared with the stable release over the same
  window, worst first;
- up to three example requests from the worst route (server errors, or the
  slowest requests when the release was rolled back for latency), each with
  the command that opens its full evidence;
- the release's open regressions and their suspected dependency.

A release rolled back by a route health monitor, an alert or a profile check
shows that reason instead of a decision. The same data is available from
`GET /v1/apps/{slug}/debug/rollout`.

Turn on [request tracing](tracing.md) to see database, cache and HTTP calls and
your app's own log lines on each example request.
