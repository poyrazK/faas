# Scaling policy

Scaling is configured per app. Keep at least one instance for latency-sensitive services, or allow zero to park idle workloads.

```bash
gregale app APP_ID scale --min 1 --max-concurrency 5
gregale app APP_ID scale --min 0 --max-concurrency 20
gregale app APP_ID scale --max-concurrency 10 --concurrency-overflow drop
gregale app APP_ID scale --concurrency-overflow queue --max-queue-wait-ms 2500
gregale app APP_ID
```

The platform uses request concurrency and resource pressure to choose a replica count in the configured range. A zero-min app parks after the plan's idle timeout and cold-wakes on the next request; see [Cold wake](cold-wake.md) for client expectations.

The API validates min/max values against the selected plan and rejects impossible combinations before changing the app. Set a realistic maximum to protect downstream services, and make handlers idempotent because retries can overlap during a scale event.

## Manifest form

For deploys, the policy can be kept beside the source in `gregale.yaml`:

```yaml
scaling:
  min_instances: 1
  max_instances: 4
  targets:
    - metric: concurrent_requests
      value: 80
    - metric: cpu
      value: 70
  scale_out_cooldown_s: 5
  scale_in_cooldown_s: 60
  concurrency_overflow: queue # queue or drop
  max_queue_wait_ms: 2500 # 0 uses the plan default
  wake_max_queue_depth: 32 # 0 uses the plan default; max 8x plan default
  wake_max_queue_wait_seconds: 30 # 0 uses the plan default; max 60
```

## Scaling signals

Each entry under `targets` states how much load **one instance** should carry.
Declaring the signals is the whole configuration surface: you do not write a
scaling rule, choose a stabilization window, or decide how signals combine.

| metric | meaning | good for |
|---|---|---|
| `concurrent_requests` | in-flight requests per instance | request apps; the signal a latency target is really reaching for |
| `rps` | requests per second per instance | steady traffic with predictable per-request cost |
| `cpu` | max CPU percent across instances | CPU-bound work whose request count understates its cost |
| `queue_depth` | backlog each worker should drain, in Gregale's own queue | job and worker apps |
| `queue_lag` | backlog still on the external broker | Kafka / AMQP / NATS / Redis Streams / SQS triggers |
| `custom` | any number your app pushes | backlogs only your app knows about |

`queue_lag` and `queue_depth` are different quantities and are not
interchangeable. A broker-backed trigger pulls at most one batch of messages
per tick, so `queue_depth` counts what Gregale has **already pulled** while
`queue_lag` counts what is **still waiting on the broker** — a
million-message Kafka backlog with a batch size of 64 shows a queue depth of
64. Scale a broker-backed consumer on `queue_lag`.

`queue_lag` reports no signal at all when no broker answers (the trigger is
disabled, the broker is unreachable, or the source does not report lag). It
does not fall back to the local queue depth, so an app declaring only
`queue_lag` simply does not scale on that axis until a reading arrives.

## Custom metrics

Every signal above is something Gregale measures *about* your app. `custom`
is the one you measure yourself — unprocessed rows in your orders table,
documents awaiting OCR, anything not derivable from request traffic. An app
can serve zero requests and still be badly behind.

Push the number, then scale on it by name:

```bash
curl -X PUT https://api.gregale.dev/v1/apps/$APP/custom-metrics/orders_pending \
  -H "Authorization: Bearer $GREGALE_TOKEN" \
  -d '{"value": 1284}'
```

```yaml
scaling:
  targets:
    - metric: custom
      name: orders_pending
      value: 100      # backlog one instance should carry
```

That scales to `ceil(1284 / 100)` = 13 instances.

**The pusher does not have to be your app.** A cron, a database trigger, or
your own infrastructure can push — which is the point: a parked app has no
process, so a signal that only a running instance could produce could never
scale you *up from zero*, which is exactly when a backlog matters most.

Things to know:

- **The value is fleet-total**, not per-instance. `value` is how much backlog
  one instance should carry, and Gregale divides.
- **Push a gauge, not a counter.** Gregale does not validate what your number
  means; a monotonically increasing counter produces monotonic scale-out.
- **Stale metrics stop counting.** A value not refreshed within the freshness
  window (returned by `GET /v1/apps/{slug}/custom-metrics`) reports no signal
  rather than its last value, so a dead pusher makes the app scale *down* on
  its other signals instead of holding the fleet at a frozen backlog. Push at
  least twice per window.
- **Names are capped per app.** Pushing a new value for an existing metric
  always works; only a *new* name can hit the cap. `DELETE` one to free a
  slot.
- Paid plans only.

When more than one target is declared, Gregale evaluates each independently
and provisions for whichever asks for the most instances. So:

```yaml
scaling:
  targets:
    - metric: concurrent_requests
      value: 100
    - metric: cpu
      value: 70
```

means "100 in-flight requests per instance is the target, **and** never let an
instance sit above 70% CPU" — if requests stay flat but CPU climbs, the app
still scales out. Each metric may appear at most once, and every value must be
greater than zero.

The single `target:` form is still accepted and behaves as a one-element
`targets` list, so existing manifests keep working unchanged.

## How apps scale back down

Scale-down is deliberately more conservative than scale-out, and the two are
not symmetric:

- **Apps with a legacy `autoscale_target_rps`** get the aggressive path: the
  scheduler computes a desired replica count from rolling request rate and
  parks the surplus above it.
- **Apps scaling on any other signal** (`concurrent_requests`, `cpu`,
  `queue_depth`, `queue_lag`, `custom`) are not offered to that path at all.
  They scale down when each instance individually exceeds its idle timeout
  (30–600 s by plan).

The practical effect is that a multi-signal app holds capacity a little
longer after a burst than an RPS-scaled one. The failure direction is
deliberate — holding an instance too long costs money, parking a busy one
costs requests.

One consequence is worth planning around if you scale on `custom`: a pushed
metric describes a backlog the platform cannot see, and a hot backlog does
not by itself generate the request traffic that keeps an instance alive. If
the work your metric describes is not driven by invocations, instances can be
admitted for the backlog, idle out, be parked, and be admitted again — churn
at the period of your idle timeout, and you are billed for each cycle. Either
make the backlog drive invocations (a queue binding does this for you), or
set `min_instances` for the window in which you expect the backlog.

## Scheduled warm windows

Every target above is reactive: it observes load that has already arrived. If
you already know when the app is busy, say so and skip the cold wake.

```yaml
scaling:
  min_instances: 0
  timezone: Europe/Istanbul
  schedules:
    - cron: "0 8 * * 1-5"
      duration: 12h
      min_instances: 3
```

"From 08:00 on weekdays, keep 3 instances warm for 12 hours." Outside every
window the app falls back to `min_instances` — here 0, so it parks overnight
and at the weekend.

A window is a **cron fire plus a duration** rather than a start/end pair,
which means a window that crosses midnight needs no special syntax:
`cron: "0 22 * * *"` with `duration: 8h` is simply a window.

Rules worth knowing:

- Schedules only ever **raise** the floor. They cannot lower one, and they
  cannot cap `max_instances` — use `max_instances` for that.
- Overlapping windows take the highest `min_instances`, so the order of the
  list does not matter.
- `cron` is evaluated in `timezone` (any IANA zone; default UTC), so daylight
  saving is handled for you: "08:00" stays 08:00 local year-round.
- `duration` accepts Go duration strings (`12h`, `90m`) and must be between
  1 minute and 7 days.
- A scheduled floor is **billed like any other warm capacity**, because it is
  warm capacity. An app with a weekday window is charged for those instances
  whether or not traffic arrives.
- Every schedule's `min_instances` counts toward your plan's `min_instances`
  entitlement and per-plan cap, so a schedule cannot buy a floor your plan
  does not include.

Schedules and targets compose: the schedule sets the floor the app starts the
window at, and the reactive targets scale above it when the day turns out
busier than forecast.

`p99_latency_ms` was accepted by older releases and never did anything — no
component ever measured a per-app p99 for scaling. It is now rejected with a
422 pointing at `concurrent_requests`, which is what rises first when an
instance starts queueing.

The CLI validates the shape and shows the nested policy in `gregale deploy
--dry-run`. The server then applies the complete policy atomically and checks
plan quotas, cooldown bounds, and workload compatibility.

`concurrency_overflow: queue` keeps requests in the bounded admission queue;
`drop` returns HTTP 429 when the app's concurrency boundary is saturated.
`max_queue_wait_ms` overrides the plan wait budget and is bounded by the API.

`wake_max_queue_depth` and `wake_max_queue_wait_seconds` independently bound
the per-app cold-wake queue. Zero keeps the plan default; the depth override is
limited to eight times the plan default and the wait override to 60 seconds.
