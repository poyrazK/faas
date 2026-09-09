# ADR-040 — per-account rate limit on the gatewayd wake path (issue #292)

Status: Accepted, 2026-07-27. Owner: @poyrazK. Closes: #292.
Related: ADR-024 (TLS observability, closes #345 — shares the
pre-instantiated 0-row exposition pattern but is unrelated in
concern); ADR-039 (traffic anomaly detection, shares the
`__other__` account-id overflow sentinel but reads from apid not
gatewayd).

- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.

## Context

The existing per-app gateway rate limit
(`pkg/gateway/ratelimit.go::Allow`, driven by
`RateLimitRPS`/`RateLimitBurst` in `pkg/api/limits.go`) bounds the
wake proxy at the appid scope. The botnet signature described in
issue #292 sidesteps that limit by rotating across a single
customer's many apps: each app individually stays under
`RateLimitRPS`, but the cross-app sum easily blows past the
account's plan budget. There is no second tier of defense today
— the per-app limiter is the only cap, and it under-features for
the cross-app threat model.

Issue #292 also names `api_key.id` as a possible key, but the
gatewayd wake path is anonymous today: `Backend.Lookup` returns
`gateway.App{ID, Plan}` only (`pkg/gateway/handler.go::ServeHTTP`
does not call any auth middleware), and `pgRouter.toApp`
(`cmd/gatewayd/backend.go:81-90`) drops `acct.ID`. Building a
per-API-key path requires either a synchronous PG lookup on every
wake request (we don't see `Authorization` on the wake path — we
forward it via `httputil.ReverseProxy`) or a wider redesign than
the issue calls for. The pragmatic key for the wake path is
**`app.AccountID`**, already joined in `pgRouter.toApp`, which
catches the botnet threat model regardless of which API key the
botnet uses.

## Decision

Add a **per-account** token bucket on the gatewayd wake path that
runs **before** the existing per-app limiter and **before** the
wake gate (the schedd gRPC `Backend.Admit` RPC). The bucket
parameters come from a new `Plan.RateLimitPerAccountRPM()` field
populated at the spec values in issue #292 (Free 50/min, Hobby
200/min, Pro 1000/min, Scale 5000/min); the bucket math divides
RPM by 60 internally to derive rps and uses RPM as the burst
ceiling, so an account can absorb `RPM` requests before the
per-minute refill kicks in.

### Amendment (2026-09-09, issue #1680): preserve the advertised app rate

The original account rows undercut a single app's advertised sustained
rate. For example, Scale advertised 500 requests/second but its shared
account bucket refilled at only 5000 requests/minute (83.3 requests/second).
The account rows are now Free 300/min, Hobby 1200/min, Pro 6000/min, and
Scale 30000/min. A table test enforces
`RateLimitPerAccountRPM >= RateLimitRPS * 60` for every plan. The account
bucket remains a finite shared abuse boundary; customers running multiple
apps share it. Both limits remain visible together in the limits API and CLI.

### Reuse `pkg/gateway.Limiter`, do not build a parallel type

The new scope shares the existing token-bucket semantics
(continuous refill, injectable clock, `Retry-After: 1`, RFC 7807
429 shape). `pkg/gateway.ratelimit.go` exposes a new public
constructor `NewLimiterWithClock(now)` for external
fresh-clock tests, refactors the existing `Allow(appID, plan)`
through a shared `allowToken(id, plan, rps, burst)` helper, and
adds `AllowAccount(accountID, plan)` + `ForgetAccount`. Two
`*Limiter` instances live on `Handler` (one per-app, one
per-account); symmetric accessors `Limiter()` /
`AccountLimiter()` mirror the existing pattern. ~80 fewer lines
vs building a parallel `AccountLimiter` type.

### Per-account FIRST; per-app SECOND; wake gate THIRD

The per-account limiter runs immediately after `Backend.Lookup`,
before the body cap, before `Limiter.Allow`, and before the
admission RPC. This ordering matters: a botnet rotating across
many apps at low per-app rps still trips the per-account bucket
inside one `ServeHTTP`, **never** reaching `Backend.Admit`. The
schedd admission queue (`pkg/sched/watchdog` 1s tick) is a
shared coordination primitive — it must not be burned by abuse.

`x-faas-rate-limit-scope: account` discriminates the new 429s
from the existing per-app ones; the same header is added to the
existing per-app 429 branch so observability tooling can split
the two populations in dashboards.

### Empty `AccountID` passes through unmetered

`AccountID` is empty only in `fakeBackend` unit tests; production
always joins it via `pgRouter.toApp`. Refusing traffic on an empty
`AccountID` would break the existing test suite and the e2e
harness. The handler logs once per process (slog DEBUG, gated by
an `atomic.Bool` flag) and continues serving — matches the
existing pattern of "pass through on missing metadata" in
`pkg/gateway/handler.go` (the host header fallback at the legacy
proxy path).

### Pre-instantiate `plan` rows under `__other__`

`gateway_per_account_rate_limited_total{account_id, plan}` is
registered as a `*prometheus.CounterVec`; the four `plan` rows
(`free`, `hobby`, `pro`, `scale`) are pre-instantiated under the
`__other__` account-id placeholder so the §12 dashboard panel
never reports "no data" on an idle box. The `__other__` sentinel
is the bounded-admission overflow bucket — if the `accountLabelSet`
admission primitive (issue #278) ever overflows on a customer
count exceeding the single-node cap, the daemon collapses the new id
to `__other__` rather than minting a fresh label. The metric
matches the `gateway_tls_on_demand_denied_total{reason}` precedent
at `pkg/gateway/metrics.go:144-146` (ADR-024 H3, PR #345).

### Alert: `FaasPerAccountRateLimitSpike`

Fleet-summed `sum(rate(gateway_per_account_rate_limited_total[5m]))
> (300 / 60)`, `for: 5m`, severity `warn`. The 300/min
fleet-total threshold matches the smallest account allowance and signals
sustained throttling that needs operator review. Per-account drill-down
lives in the dashboard and runbook
(`topk(20, sum by (account_id, plan) (rate(... [5m])))`) rather
than in the alert expression — keeping the rule evaluation cost
bounded by fleet cardinality, not by customer cardinality.
Companion runbook: `docs/runbooks/FaasPerAccountRateLimitSpike.md`.

## Consequences

- `pkg/api/limits.go` carries the new `RateLimitPerAccountRPM`
  field on `Limits` (one new struct field, one new accessor on
  `Plan`). Single source of truth for all plan quotas.
- `pkg/gateway/ratelimit.go` adds `AllowAccount`, `ForgetAccount`,
  `NewLimiterWithClock`; refactors `Allow` through the shared
  `allowToken` helper. ~30 net new lines.
- `pkg/gateway/handler.go` adds the `AccountID` field to `App`,
  the `accountLimiter` field to `Handler`, and the per-account
  429 branch in `ServeHTTP`. The branch runs **before** the wake
  gate so abuse traffic doesn't burn schedd.
- `cmd/gatewayd/backend.go` plumbs `AccountID` in `pgRouter.toApp`.
- `cmd/gatewayd/main.go` SIGHUP now calls both `Limiter().ForgetAll()`
  and `AccountLimiter().ForgetAll()`.
- `pkg/gateway/metrics.go` carries the new
  `gateway_per_account_rate_limited_total{account_id, plan}`
  counter and a nil-receiver-safe `ObserveAccountRateLimit`
  helper.
- One new alert (`FaasPerAccountRateLimitSpike`) and one new
  runbook (`FaasPerAccountRateLimitSpike.md`). The
  `cmd/e2e/runbooks_e2e_test.go` `minRunbooks` count moves from
  11 to 12.
- `docs/faas_implementation_spec.md` §4.1 amended to name the
  per-account limit.

### Account-id cardinality

O(10 000 × 4) = 40 000 series max, bounded by the current
single-node account cap (~10k) plus 4 pre-instantiated `plan` rows.
The alert and runbook document the cardinality audit path; if
customer count crosses 10k, the cap is lifted in lockstep with
the existing `accountLabelSet` admission primitive (issue #278).

### Plan-change cache invalidation

`apid.UpdateAccountPlan` does not emit pg_notify today; the
60-second `FlushRoutes` sweep keeps the apps cache side correct.
The limiter side is safe because `AllowAccount` re-reads
`Plan.RateLimitPerAccountRPM()` on every call and overwrites
`b.rps`/`b.burst` on the next `allowToken` call against that id.
This is the same lazy-flip behavior as the per-app limiter
(`pkg/gateway/ratelimit.go`).

### Test patterns

- **Wire-layer** (`pkg/api/limits_test.go`): extend
  `TestPlanLimitsMatchSpec` to pin the new field; add
  `TestPlanRateLimitPerAccount` plus the cross-field invariant
  `TestPlanAccountRateSupportsOneAdvertisedApp`.
- **Bucket math** (`pkg/gateway/gateway_test.go`): tests cover
  burst/refill, account isolation, unknown plans, plan changes, and
  five simulated minutes at Scale's advertised 500 requests/second.
- **Handler** (`pkg/gateway/handler_test.go`):
  `TestAccountRateLimitReturns429` asserts the new 429 path
  carries `Retry-After: 1` and
  `x-faas-rate-limit-scope: account`, and that the new
  counter increments.
- **Load gate** (`pkg/gateway/handler_load_test.go`): the
  existing 1k rps hot-path SLO test installs
  `WithAccountLimiter(unlimitedAccountLimiter())` so the load
  window doesn't trip the new per-account bucket.
- **Backend integration** (`cmd/gatewayd/backend_test.go`):
  `TestAccountRateLimit_ThreeOhOneReturns429` wires the production
  handler through `pgRouter + MemStore + PGBackend` with a frozen
  clock, fires 301 sequential requests, and asserts the 301st
  returns 429. The frozen clock prevents the Free bucket from
  refilling mid-loop.
- **Metrics** (`pkg/gateway/metrics_test.go`):
  `TestMetricsAccountRateLimitedRegistersAndPreInstantiates` +
  `TestMetricsAccountRateLimitedNilSafe`.

## Alternatives Considered

- **Per-API-key wake-path key** — rejected: the gateway doesn't
  see API keys on wake paths (it forwards `Authorization` via
  `httputil.ReverseProxy` without inspection). Building this
  would require a synchronous PG lookup on every wake request
  or a wider redesign than #292 calls for. The pragmatic per-
  account key catches the same botnet signature.
- **Per-IP fallback** — rejected: existing
  `pkg/middleware/authlimit.go` already enforces per-IP on the
  apid auth surface, and stacking a per-IP gate on the wake path
  would double-count customers on NAT (multiple devices on one
  residential connection → single IP).
- **Single fleet-wide limiter** — rejected: doesn't bound the
  per-customer blast radius. A single noisy customer could
  starve the wake queue for everyone.
- **Account refill below one app's advertised rate** — the original
  issue #292 rows used this shape. Issue #1680 demonstrated that it
  made the per-app RPS contract unsustainable and the amendment above
  replaced it with a tested floor.

## Open follow-ups

1. `notify_account_changed` pg_notify for instant plan-flush
   (today the 60s `FlushRoutes` sweep covers it).
2. `ForgetAccount` admin endpoint for per-customer bucket reset
   without SIGHUP's full drop. Tier-3 work.
3. Customer-visible dashboard quota UX (the "you've used N of M
   requests this minute" view). Tier-2, tied to a billing visible-
   telemetry decision still pending (gap G2 lean in §17 — sealed
   at-rest envelopes suffice for per-minute observation but not
   for customer-visible replay).

## Cross-references

- ADR-024 (TLS observability): shares pre-instantiated 0-row
  exposition pattern (`gateway_tls_on_demand_denied_total{reason}`).
- ADR-039 (traffic anomaly detection): shares
  `__other__` overflow semantics. Reads from apid not gatewayd;
  no shared code.
- ADR-036 (instance metrics cardinality): the per-account
  counter cardinality (40 000 max) is bounded by the
  `accountLabelSet` admission primitive defined there.
- §4.1 (gatewayd rate-limit contract) — amended in this PR.
- §11 (abuse-vector observability) — the alert's coordination
  signal logic is the §11 cross-component counterpart of the
  in-bucket 429 budget.
- §12 (Prometheus dashboards) — the new counter lands on the
  existing `gateway_rejections` panel; the `__other__` row keeps
  the panel from going dark on an idle fleet.
