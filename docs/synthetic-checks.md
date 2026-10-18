# Synthetic checks

A synthetic check requests a path on your app on a schedule, from outside
the app, and records whether it succeeded and how long it took. It catches
what traffic-based metrics cannot: an app nobody has called for an hour can
still be broken by a bad deploy, an expired credential, or a DNS change
(ADR-748).

Each probe goes the way a user's request does — DNS, TLS, the public edge,
and a wake if the app is parked — to your app's own hostname. Checks are
available on Hobby and above.

## Create a check

```sh
gregale synthetics create --app shop --name health --path /healthz
gregale synthetics create --app shop --name home --path / --method HEAD \
  --expect-status 200 --every-minutes 15
gregale synthetics list --app shop
gregale synthetics pause --app shop CHECK_ID
gregale synthetics resume --app shop CHECK_ID
gregale synthetics rm --app shop CHECK_ID
```

The API equivalent is `POST /v1/apps/{slug}/synthetics`, with `GET`,
`PATCH` (pause or resume) and `DELETE` on `.../{id}`.

| Field | Values |
|---|---|
| `path` | A path on the app, query string allowed: `/healthz`, `/api/ping?deep=1`. The host is always the app's own. |
| `method` | `GET` (default) or `HEAD`. |
| `expected_status` | One exact status, or omit to accept any 2xx. Redirects are not followed, so a 301 is reported as a 301. |
| `timeout_ms` | 1,000 to 30,000 (default 10,000), including any wake. |
| `interval_seconds` | 300, 900 or 3600 — every 5, 15 or 60 minutes. |

An app can have up to 5 checks.

## See results

```sh
gregale synthetics status --app shop CHECK_ID
```

```
health — GET https://shop.gregale.dev/healthz every 5 minutes, expecting 2xx
  Uptime:   99.65% (24h, 288 runs) · 99.90% (7d)
  Latency:  p95 840 ms over 24h, including wakes
  Recent runs:
    2026-10-10T12:05:00Z  ok              200  92 ms
    2026-10-10T12:00:00Z  FAIL status     503  31 ms
```

A run fails with one of: `status` (a response with an unexpected code),
`timeout`, `dns`, `connect`, `tls`, or `other`. Runs are kept for 7 days.
Latency is measured end to end, so a run that woke a parked app includes the
wake. `GET /v1/apps/{slug}/synthetics/{id}` returns the same figures in
`results`.

Checks run from Gregale's control plane. Requests carry the user agent
`Gregale-Synthetics/1`, so you can recognise them in your logs.

## Alert on a check

Two alert metrics watch one check, named with `synthetic_check_id`
(`--synthetic-check` in the CLI):

```sh
# Two failed runs in a row, ignoring a single blip.
printf '%s\n' "$ALERT_SECRET" | gregale alerts add --app shop --name "health failing" \
  --metric synthetic_check_consecutive_failures --synthetic-check CHECK_ID \
  --comparison gte --threshold 2 --window-spec 1h \
  --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin

# p95 latency of successful runs above 2 s over an hour.
printf '%s\n' "$ALERT_SECRET" | gregale alerts add --app shop --name "health slow" \
  --metric synthetic_check_latency_p95_ms --synthetic-check CHECK_ID \
  --comparison gt --threshold 2000 --window-spec 1h \
  --webhook-url https://example.com/hooks/gregale --webhook-secret-stdin
```

- `synthetic_check_consecutive_failures` counts failed runs since the last
  success; the rule's `window_spec` is required but ignored.
- `synthetic_check_latency_p95_ms` covers successful runs in the window and
  includes wakes, so set the threshold above your app's wake time.
- A check with no runs yet leaves the rule `unknown`. Deleting the check
  deletes its rules.

The dashboard's app page lists every check with its uptime, p95 latency and
last result under **Synthetic checks**.

## What checks cost

A probe is an ordinary request and is billed like one. If your app is
already running because of real traffic, a probe adds nothing. If the app is
parked, the probe wakes it, and it stays up for your plan's idle timeout
before parking again. For an app with **no other traffic**, one check costs
roughly:

| Plan (idle timeout) | Every 5 min | Every 15 min | Every 60 min | Included per month |
|---|---|---|---|---|
| Hobby (60 s) | ~37 GB-h | ~12 GB-h | ~3 GB-h | 50 GB-h |
| Pro (300 s) | ~366 GB-h (always warm) | ~122 GB-h | ~31 GB-h | 250 GB-h |
| Scale (600 s) | ~726 GB-h (always warm) | ~484 GB-h | ~121 GB-h | 1,500 GB-h |

Figures use the plan's RAM plus 8 MB per running second over a 30-day month.
On Pro and Scale, a check at or below the idle timeout keeps the app warm
continuously, which also removes cold starts for real users. Choose a
longer interval for an app that should park. The shortest interval is
5 minutes, so a check can never keep a Hobby app awake all the time.
