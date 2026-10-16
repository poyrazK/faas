# Route advisor

`gregale routes advise` reads your app's recent traffic and suggests
[edge rules](edge-rule-trace.md) for routes that would benefit from one, with
the evidence and an estimate of what the rule would have done over the same
window:

```sh
gregale routes advise my-api
gregale routes advise my-api --since 3d --cache-max-age 300
gregale routes advise my-api --apply 3f9a1c2b7d4e
```

The advisor only reads. Applying a suggestion creates its rules **disabled**,
one per hostname (your `*.gregale.dev` hostname and every verified custom
domain), so you can review them before turning them on with
`gregale edge-rules update ID --enable`. Add `--enable` to `--apply` to create
them enabled. The API is `GET /v1/apps/{slug}/routes/advice`.

## Suggestions

| Kind | Suggested when | Estimate |
| --- | --- | --- |
| `cache` | A `GET` route with a fixed path where at least 95% of requests are anonymous and succeed, and at least 20% would be repeats inside the cache lifetime. | Requests served from the edge cache and wakes avoided, because a cached response is served without waking a parked app. |
| `async` | A `POST` route where at least 1% of requests (and at least 5) time out, or the p95 latency is 10 seconds or more. Needs a plan with async invocation and an app that accepts request invocations. | Timed-out requests that would instead run as durable invocations with retries. |
| `throttle` | One API consumer sends at least half of a route's requests while other consumers also use it, and almost no traffic is anonymous. | Requests from that consumer that would get `429`. The per-consumer limit is twice the next busiest consumer's peak minute, so no other observed consumer reaches it. |

A route needs at least 200 requests in the window. The advisor looks at your
50 busiest routes across all deployments, and skips a route that already has a
rule of the same kind, enabled or disabled, so an applied suggestion does not
come back.

Each suggestion has a stable ID: the same kind, method and route always get
the same ID. Rule bodies depend on the window and the cache lifetime, so apply
with the same `--since` and `--cache-max-age` you listed with.

## Settings

Two suggestions change app settings instead of adding edge rules:

| Kind | Suggested when | Applying it |
| --- | --- | --- |
| `wake_ahead` | Your app is not opted in and the gateways measured a service it usually calls right after a cold wake, usually parked. | Turns on [service wake-ahead](service-wake-ahead.md). |
| `priority` | No route priorities are set and, in the last 24 hours, at least 10 requests were turned away or lost their queue place while every instance was busy. | Saves the busiest routes that change data (up to three) as critical [route priorities](route-priority.md). Review them first. |

`gregale routes status` marks routes with a pending suggestion
(`advice_available`) and counts all suggestions.

## Is it safe to cache?

For anonymous `GET` requests on routes without a cache rule, the gateways
compare each response with the previous response for the same URL. They
keep only the outcome, never the body: the same or changed, and how long
after the previous request it came. A cache suggestion then shows what was
measured:

```text
  Content: 633 responses compared; 600 repeats, 3 changed; safe max-age 3600s
```

- **Safe max-age** is the longest of 60 s, 300 s and 3600 s at which, among
  at least 50 repeat requests (10 of them at that spacing), at most 1% got a
  different body. A cache rule with that lifetime would rarely serve a stale
  answer.
- A route is **not suggested** when more than 5% of repeats under a minute
  apart got a different body, or when most responses can't be cached at all
  (`Set-Cookie`, `Cache-Control: no-store` or `private`, `Vary`, or bodies
  over 1 MiB).
- Until enough repeats are seen, the suggestion says so and keeps the usual
  warning. Each gateway compares on its own and forgets after a restart, so
  quiet routes take longer to measure.

Routes that already have a cache rule are checked too: each time the edge
refills its copy, the gateway compares the new body with the old one. A rule
that served stale bodies, or one far shorter than the content's lifetime,
becomes a `cache_stale` or `cache_too_short` [route insight](route-insights.md).

## Reading the estimates

Estimates replay your retained telemetry; they are not guarantees.

- **Cache.** The cache lifetime defaults to 60 seconds, the default for a new
  cache rule. Use `--cache-max-age` (1–3600 seconds) to see what a longer
  lifetime would do; the suggested rule uses the same value. Requests carrying
  `Authorization` or `Cookie` headers always bypass the cache, and the
  estimate treats every query string as the same response, so it is an upper
  bound. Apply a cache rule only if the response is the same for every caller
  and changes less often than the lifetime.
- **Async.** Callers receive `202 Accepted` with an invocation ID instead of
  the response body. Update your clients before enabling the rule.
- **Throttle.** The limit applies to every consumer of the route. Requests
  without a consumer identity share one bucket.

## Plans and data

The advisor uses request telemetry, so it follows the same plan gate and
retention as request analytics: the window (default 7 days) is shortened to
your plan's retention and the response says so. Free plans have no request
telemetry. Only routes recorded in telemetry are considered; consumer
identities in throttle suggestions are the request-time API consumer IDs.

To see which routes keep your app awake, and what removing each would save,
run [`gregale routes awake`](route-awake.md).
