# ADR-20261114153000: Prime a deployment's first init snapshot

- Status: Accepted (owner decisions 2026-11-14: healthcheck path, later a
  `warmup:` path; 20 requests capped at 5 s; all apps; on failure capture
  unprimed and warn)
- Date: 2026-11-14
- Amends: ADR-055/071 (snapshot tiers), ADR-20261110180000 (warm tier opt-in)

## Context

A deployment's first init snapshot is captured right after readiness,
before the app has served a request. Every wake restores that memory, so
every wake runs each request code path for the first time again: classes
load, the JIT compiles, pools open. The warm tier (ADR-055/071) avoided
this by capturing after real traffic, but it freezes request-derived state
(sessions, caches, `/tmp`) into every later wake, and since
ADR-20261110180000 it is opt-in.

The 2026-11-09 benchmark (`docs/ops/evidence/20261109-warm-vs-init-snapshot`)
measured the gap. For Go, Node and Python the warm snapshot saved 0-80 ms,
inside noise. For Java it saved 0.5-0.8 s of the first request after each
wake, and a synthetic warm-up of a few GETs before the init capture captured
all of that gain.

## Decision

1. **When.** Only the deployment's prime capture (`snapshotAndParkPrime`,
   site `prime`) is primed. A later park reuses that snapshot or recaptures
   after real traffic and is not primed.
2. **What.** schedd calls the new vmmd RPC `PrimeInstance` with a path, 20
   requests and a 5 s budget. The path is, in order: the app's
   `lifecycle.warmup` path; its HTTP readiness path (`startupProbeForApp`);
   the image's declared health path; `/` for an HTTP/1 app whose readiness
   is only a TCP connect, which is most apps, since few declare a health
   path.
   vmmd clamps both to those limits and sends the GETs one at a time to the
   guest's port 8080, the address readiness probes. Redirects are not
   followed, and the burst's private transport is closed before it returns,
   so no host-side connection outlives it into the snapshot. Apps with gRPC
   readiness, HTTP/2 or gRPC framing, or a port routed as raw TCP are not
   primed unless they declare `lifecycle.warmup`.
3. **Who.** All apps and plans. Priming runs in vmmd on the instance's own
   node and adds at most 5 s to a deploy, not to any wake.
4. **Failure never fails a deploy.** An older vmmd (Unimplemented), an RPC
   error, or a burst with no 2xx response captures the snapshot unprimed and
   logs a warning. `schedd_init_snapshot_prime_total{outcome}` counts
   `primed`, `no_http_path`, `unsupported`, `no_success` and `failed`.
5. **`lifecycle.warmup`.** An app may name its own priming path
   (`lifecycle.warmup.path` in gregale.yaml, `warmup` on the app API). It
   replaces the readiness path, so a cheap `/healthz` that warms only the
   runtime and server can be swapped for a path that runs real handler code.
   It also primes apps whose readiness is gRPC or raw TCP. It is an app
   lifecycle setting like `before_checkpoint`, stored in the app manifest,
   validated as an absolute path, allowed for request and service execution
   modes, and applied from the next deploy; an empty path clears it.

## Consequences

- Java and other JIT runtimes wake with compiled request code; the
  benchmark puts the first-request gain at 0.5-0.8 s per wake.
- Priming requests run app code before the snapshot. A readiness handler
  with side effects runs up to 20 more times per deploy. Request state those
  GETs create is captured like any other startup state; it is the app's
  own readiness path, not customer traffic.
- Priming GETs do not pass through the gateway, so they are not billed,
  metered or counted in request analytics.
- No lifecycle state changes: the instance stays RUNNING during the burst,
  held by a vmmd operation flight so a quiet pause cannot interrupt it.
- Release skew: a new schedd with an old vmmd records `unsupported`; an old
  schedd never calls the RPC.

## Amendment 2026-11-19: functions are primed only on a declared path

Prod found a function's handler invoked 20 times per deploy with a synthetic
`GET /`, no invocation ID and no platform headers: a function has no
readiness path, so the burst fell through to `/`, and every path of a
function is its handler. A deployment with a function handler is now primed
only when it declares `lifecycle.warmup`; otherwise the outcome is
`no_http_path`. HTTP apps keep the `/` fallback, and the docs now say that the
burst runs customer code and how to point it at a harmless path.

Amendment (2026-11-20): the function check also covers functions discovered
from config in code, which deploy as `type: function` without a recorded
handler. The app type now decides as well as the deployment's handler.
