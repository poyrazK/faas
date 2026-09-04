# ADR-093: End-to-end request budgets via `pkg/reqbudget`

- **Status**: Proposed
- **Date**: 2026-08-12
- **Audience**: gateway, apid, schedd, observability, and platform teams; any service that calls into vmmd gRPC from a request-scoped goroutine

## Context

Customer HTTP requests can hang for up to **300 seconds** on the response leg (`gatewayd-public` stdlib `WriteTimeout=300s`) and **60 seconds** on the read leg — a full minute of pinned listener / goroutine / socket resources in the worst case for what should be a sub-second `POST /payment`. The only enforced wall-clock budget on customer-facing traffic today is the stdlib `WriteTimeout` on `gatewayd-public` (`cmd/gatewayd-public/main.go:340`); `gatewayd-internal` carries a per-plan `WriteTimeout` (`cmd/gatewayd-internal/run.go:1416-1444`); `apid` carries none.

Per-call `context.WithTimeout(r.Context(), N)` ceilings exist scattered through the request path:

| Site | Ceiling |
|---|---|
| `pkg/gateway/handler.go:1392-1398` (`verifyJWTWithDeadline`) | 5s (`EdgeRuleJWTVerifyTimeoutDefault`) |
| `pkg/gateway/forwardproxy.go:264` (`fwdStreamOnceWithEvents`) | 910s |
| `pkg/gateway/forwardproxy.go:500` (`rawStreamOnceWithEvents`) | 24h (`rawStreamSessionDeadline`) |
| `cmd/apid/handlers_dashboard*.go` (PromQL) | 3s |
| `cmd/apid/handlers_admin_billing.go:51` (billing SDK) | 30s |
| `cmd/apid` sync-invoke long-poll | 5-30s |
| `pkg/gateway/gate.go:160-168` (WakeGate leader) | 30s — `context.Background()`-detached |

Each is fresh-on-top-of-`r.Context()`. None propagates remaining time. The CLI's `pollBuildStatus` (`cmd/gregale/commands2.go:2566-2614`) is the only in-tree example of `remaining := time.Until(end)` derivation.

There is no `request budget` / `deadline propagation` / `remaining budget` abstraction anywhere in the request-serving path. `grep -rn "timeout budget|deadline propagation|remaining budget"` across `*.go`/`*.md` returns zero hits. `pkg/api/limits.go:567` `TailTimeoutS` is per-task, enforced inside the guest, not end-to-end HTTP.

## Decision

Introduce `pkg/reqbudget`. The package carries an immutable `Budget` value struct through `context.Context`. Each downstream hop wraps its outgoing ctx via `min(parent.Remaining - overhead, ownCeiling)`. The deadline can only ever shrink, never grow.

Three layers:

1. **Edge budget setter** — `BudgetMiddleware` (`pkg/reqbudget/middleware.go`) is installed at the public listener of `gatewayd-public` (PR 2) and `apid` (PR 3). The middleware stamps a per-request `Budget` onto `r.Context()`, observes the outcome (`set|exceeded|cancelled`) at handler completion, and writes a 504 RFC 7807 envelope when the deadline fires before the inner handler committed a response.

2. **Per-hop helper** — `Budget.WithOverhead(ctx, "db", DefaultOverheadDB)` is the workhorse. Child deadline = `min(parentRemaining - cost, parentCeiling - Σ(overhead))`. Used at every downstream call: DB, gRPC, outbound HTTP, streaming. Default overheads live in `pkg/api/limits.go` (`DefaultOverheadDB=10ms`, `DefaultOverheadGRPC=5ms`, `DefaultOverheadHTTP=20ms`, `DefaultOverheadStream=50ms`, `DefaultOverheadQueue=5ms`).

3. **Edge-rule kind=budget** (PR 2) — sits next to `kind=validate`, `kind=ip`, `kind=geo` from ADR-091. Per-(hostname, path, method) override of the platform's request budget. Schema in `pkg/api/dto.go`; resolution path `pkg/gateway.(*Handler).applyEdgeRuleBudget`, mirroring `applyEdgeRuleIP`.

Defaults in `pkg/api/limits.go`:

| Constant | Value | Meaning |
|---|---|---|
| `RequestBudgetDefault` | 3s | gatewayd-public default budget |
| `RequestBudgetMax` | 30s | absolute upper bound on any per-request budget |
| `RequestBudgetApidDefault` | 5s | apid default budget |
| `Limits.RequestBudgetMs` / `Limits.RequestBudgetMaxMs` | 0 → fall back | per-plan overrides |

When the inbound `r.Context()` carries an earlier deadline (stdlib `http.Server.WriteTimeout`), `WithRemaining` honors it — the tighter one wins. When no `Budget` is attached to the inbound ctx (internal goroutine, admin path), `WithOverhead` and `WithCeiling` are identity no-ops — call-sites without a budget don't change behavior.

## Consequences

### Positive

- Bounded resource pin per request: a slow hop short-circuits the rest of the chain instead of consuming the full server timeout.
- Predictable latency tail: customers get the platform's SLO guaranteed without writing propagation code.
- Observability: four new metric families
  - `gateway_request_budget_seconds{route, endpoint, outcome}` histogram (buckets `[5ms, 25ms, 100ms, 500ms, 1s, 2s, 5s, 10s, 30s]`)
  - `gateway_request_budget_exceeded_total{route, endpoint, hop}` counter
  - `apid_request_budget_seconds` + `apid_request_budget_exceeded_total` (same shape, apid namespace)
- Per-route audit trail on the `Budget.Overheads` slice — the failing hop is the last entry on the slice at exceed-time and is the `hop=...` label on the counter.

### Load-bearing exceptions (explicitly NOT changed)

- **`WakeGate` leader** (`pkg/gateway/gate.go:156-168`) keeps `context.Background()` + `g.ttl=30s` for the leader. The single-flight coalescing invariant (spec §4.1) requires that the wake outlive the triggering request — a client disconnect after triggering a wake must not abort the wake for everyone else. Followers piggyback on the leader's stream and honor the budget implicitly via `r.Context()`.
- **JWT verify** (`verifyJWTWithDeadline`, `pkg/gateway/handler.go:1392`) keeps its 5s fresh cap (`EdgeRuleJWTVerifyTimeoutDefault`). The budget becomes the *minimum* of (parent budget, 5s).
- **`fwdStream`** keeps 910s; **`rawStream`** keeps 24h. Both are absolute ceilings; the budget only tightens them.
- **`setupStreamingWriter`** (`pkg/gateway/handler.go:2258`) keeps its sliding per-flush deadline. The budget bounds the *total* wall-clock the writer stays open (cooperation, not conflict — flushInterval × N ≤ budget).
- **`vmmdgrpc/forward.go:464` HTTP/1.1 hop** doesn't propagate ctx today — known follow-up, out of scope for this ADR.
- **stdlib `http.Server.WriteTimeout`** (300s on public, per-plan on internal) stays as the outermost structural safety net (anti-slowloris / hung-handler safety, not business latency).

### Observability

Per-endpoint log line on attach / exceed / cancel, field-order fixed for `awk`:

```
INFO  budget_attached  budget_ms=3000 elapsed_ms=2 remaining_ms=2998 route=forward endpoint=POST:/payment hop=gateway
INFO  budget_exceeded  budget_ms=3000 elapsed_ms=3002 remaining_ms=0 route=forward endpoint=POST:/payment hop=http observed_ms=3002
WARN  budget_cancelled budget_ms=3000 elapsed_ms=1432 remaining_ms=1568 route=forward endpoint=POST:/payment reason=client_disconnect
```

### PR strategy

Three stacked PRs (mirrors `tier-a7 PR-cluster strategy` from memory):

1. **PR 1 — library + ADR + constants.** Pure library: `pkg/reqbudget/{budget,overhead,metrics,middleware,doc}.go` + tests, `pkg/api/limits.go` append, this ADR. No production wiring change.
2. **PR 2 — `gatewayd-public` + `kind=budget` edge rule + DB migration.** Customer-facing path.
3. **PR 3 — `apid` + dashboard/billing/sync-invoke + `Plan.RequestBudgetMs` plumbing.**

## Alternatives considered

- **Replacing stdlib `WriteTimeout`.** Rejected — structural vs business latency. Stdlib timeout catches bad-client-idle (slowloris, half-open sockets); the request budget catches slow downstream.
- **Decorating every `http.RoundTripper`.** Rejected — too many wrappers already; per-call-site `context.WithTimeout` is auditable and grep-able.
- **A global deadline-on-everything helper.** Rejected — must coexist with hop-specific ceilings (JWT 5s, fwdStream 910s, rawStream 24h).
- **Per-app budget in apid config (not edge rule).** Rejected — loses per-(hostname, path, method) targeting the user asked for. Edge rules are the right surface.
- **Per-RPC deadline on grpc.Dial.** Considered — `pkg/wire.DialContext` (memory note from PR-823) already exists; the per-RPC deadline there is the hop ceiling for gRPC calls. PR 2/3 will route through it.

## References

- `pkg/reqbudget/budget.go` — Budget value, ctx carrier, WithRemaining / WithOverhead / WithCeiling
- `pkg/reqbudget/middleware.go` — `BudgetMiddleware`, RFC 7807 envelope
- `pkg/reqbudget/metrics.go` — Prometheus registration (namespace = "gateway" or "apid")
- `pkg/reqbudget/overhead.go` — 5 `DefaultOverhead*` constants
- `pkg/api/limits.go` — `RequestBudgetDefault`, `RequestBudgetMax`, `RequestBudgetApidDefault`, `DefaultOverheadDB/GRPC/HTTP/Stream/Queue`, `Limits.RequestBudgetMs/MaxMs` accessors
- ADR-091 — `kind=validate` / `kind=ip` / `kind=geo` edge-rule surface (the precedent for `kind=budget`)
- spec §6.3 — wake latency budget (cold-boot, **not** this ADR's domain)
- spec §4.1 — `WakeQueueCap=512`, `WakeQueueTTLSeconds=30`
## Amendment (2026-09-03): gatewayd-public stamps a backstop, not a policy

**The `kind=budget` edge rule could not widen a budget. It could only
tighten one.** This amendment changes `gatewayd-public`'s configured
budget from `api.RequestBudgetDefault` (3 s) to `api.RequestBudgetMax`
(30 s), making it a liveness backstop rather than a policy decision.

### What was wrong

PR 2 wired the middleware at the public edge with `Default:
api.RequestBudgetDefault` and this comment:

> Budgets come from the edge-rule kind=budget match (resolved deeper
> in the chain) or fall back to api.RequestBudgetDefault.

The second clause is accurate; the first never took effect.
`reqbudget` derives a **child** budget from whatever is already on the
context (`WithCeiling` / `WithOverhead` both clamp against the
parent's remaining time — by design, per the original Decision).
`gatewayd-public` does not resolve the app, so it cannot see a
`kind=budget` rule; it stamps the 3 s default first, and
`applyEdgeRuleBudget` then runs one hop later in `gatewayd-internal`
against an already-3 s parent.

Measured on the europe-west3 deployment at 1000 requests /
1000 concurrency, with a 25 s `kind=budget` rule confirmed applied
(`budget_ms:25000, source:rule` on 713 of 1000 requests):

```
upstream latency  p50 2948  p90 3000  p99 3013  max 3121  (ms)
```

Nothing crossed ~3.1 s. In the same window `gatewayd-public` logged
612 round-trip failures, all `context deadline exceeded`, matching the
612 client-visible failures exactly. The compute layer had answered
953 of 1001 requests with 200.

### Decision

`gatewayd-public` stamps `api.RequestBudgetMax`. Every request it
forwards lands on a hop that stamps its own authoritative budget:

- **App data plane** → `gatewayd-internal`, whose
  `applyEdgeRuleBudget` *always* stamps (rule match, else the plan
  default) and owns the 504 + `request_budget_exceeded` envelope.
- **Control-plane API** → `apid`, which installs its own middleware at
  `api.RequestBudgetApidDefault` (5 s).

The edge therefore defers to whichever hop can actually see the
customer's plan and rules, and keeps only the platform ceiling as a
liveness guard. This subsumes the previous sync-invoke `DefaultFor`
carve-out, which returned exactly this value for the same reason.

### Trade-off

A wedged `gatewayd-internal` can now pin a public connection for up to
30 s instead of 3 s. Accepted: the failure mode it replaces is worse
(silently capping every customer's configured budget), the ceiling is
still bounded, and the transport's `ResponseHeaderTimeout` remains an
independent guard. The per-plan 3 s default is unchanged — it simply
now applies where it can be overridden.

### Not changed

The platform default stays `RequestBudgetDefault = 3 s` and the
ceiling stays `RequestBudgetMax = 30 s`. This amendment moves *where*
the default is enforced, not its value. A customer who sets no
`kind=budget` rule still gets 3 s, stamped by `gatewayd-internal`.
