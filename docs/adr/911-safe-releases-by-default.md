# ADR-911 · Safe releases by default

- **Status:** accepted; default-on behind staging and low-traffic drills (see Rollout)
- **Date:** 2026-10-09
- **Amends:** [ADR-200](200-auto-rollback-on-every-plan.md) ("the opt-in stays off by default"), [ADR-122](122-safe-releases.md) canary progression
- **Follows:** [ADR-199](199-rollouts-on-every-plan.md), [ADR-625](625-first-wake-5xx-rollback-from-request-telemetry.md)
- **Decision:** A production release of an app that already serves traffic rolls out behind the balanced health-gated canary with first-wake 5xx rollback unless the deploy or the app says otherwise. A canary stage held only because too few requests arrived advances after a bounded wait when no negative evidence was observed.
- **Why:** The API-hosting roadmap names "one opinionated production path with automatic health gates and rollback enabled by default" as the highest-priority release-safety gap. Every piece already exists (canary ladder, always-on circuit breaker, ADR-625 rollback), but only customers who know to type `--safe` get them, and GitHub App pushes could not ask for them at all.
- **Rejected alternatives:** A CLI-only default leaves GitHub pushes, the API, Terraform, and the Action unprotected. Rejecting a deploy when the canary worker is down turns a safety default into an outage during the incidents where a hotfix matters most. Keeping the unbounded sample-size hold would strand most scale-to-zero apps at 1% forever.

## Context

`gregale deploy --safe` stamps `canary.preset=balanced` and
`rollback_on_5xx=true`. The meterd circuit breaker
([`pkg/canary/circuit_breaker.go`](../../pkg/canary/circuit_breaker.go))
compares the candidate with its stable predecessor on 5xx rate, p95 and
cold-boot latency, CPU per request, managed dependency errors, and OOM kills.
It aborts a clear regression through APID's exact rollout recovery.

Two things kept this from being the default:

1. **It was opt-in everywhere.** ADR-200 deliberately left `rollback_on_5xx`
   off by default, and GitHub App push deploys
   ([`cmd/apid/githubd_bridge.go`](../../cmd/apid/githubd_bridge.go)) carried
   no rollout fields at all.
2. **Low-traffic apps never finished.** The breaker holds until both sides
   have `CircuitBreakerMinRequests` (20) requests. At a 1% stage that needs
   roughly 2,000 requests in the window. A scale-to-zero API that serves a
   few requests a minute held at 1% indefinitely, which is why a safe
   default could not ship before this ADR.

## Decision

### Default release policy

apid fills in rollout fields a deploy request left unset
([`cmd/apid/default_release_policy.go`](../../cmd/apid/default_release_policy.go)).
It never overrides an explicit value. The default applies when all hold:

- the app's `release_policy` is `safe` (the unset value);
- the app is request-mode and not a PR preview app (services keep schedd's
  readiness-gated rollout; workers and jobs serve no request traffic);
- the deploy targets the default scope (named project environments are
  unchanged);
- the app already has a live deployment in that scope. A first deploy has
  nothing to compare with or roll back to.

Then:

- an unset `rollback_on_5xx` becomes `true`;
- an unset rollout (no `canary` and no `traffic_percent`) becomes the
  `balanced` canary, if the safe-release worker lease is ready.

Each piece is filled in only when the plan gate that later validates it
(`RollbackOn5xxAllowed`, `TrafficSplitAllowed`) allows it. Both are true on
every plan today (ADR-199/200); if an operator re-tiers either, the default
quietly drops that piece rather than turning flag-free deploys into 403s.

Every deploy entry point applies it: image, multipart source, source-ref,
source tarball, resumable upload sessions, and GitHub App push deploys.

### Opting out

- Per deploy: `--canary-preset none` (or `canary: {"preset": "none"}`) skips
  the canary; `--rollback-on-5xx=false` skips the rollback; an explicit
  `--traffic-percent` is honoured as written.
- Per app: `gregale app <slug> --release-policy immediate`
  (`PATCH /v1/apps/{slug}` with `{"release_policy":"immediate"}`) restores
  the pre-ADR behaviour for every deploy of that app. The field lives in the
  app manifest JSON; no migration is required.

### Fail open when the canary worker is unavailable

An explicit canary still fails with `safe_release_unavailable` when meterd's
safe-release lease is not ready. A defaulted canary instead falls back to an
immediate cutover, keeps the rollback opt-in (evaluated by apid from request
telemetry per ADR-625, independent of meterd), and logs the fallback. A
default must never fail a deploy that would have succeeded without it.

### Bounded low-traffic hold

`api.CanaryLowTrafficMaxHold` (5 minutes) bounds a hold that exists only
because of sample size. When a stage's dwell time plus that bound has passed
and the breaker's only objection is "insufficient request samples" or
"insufficient CPU/request samples", the stage advances if:

- the candidate returned zero 5xx responses in the window, and
- the OOM signal is readable and recorded zero kills.

Unavailable signals (OOM, CPU/request, dependency errors) still hold
indefinitely, and a regression with enough samples still aborts. The
advance is exported as
`meterd_canary_progression_circuit_breaker_total{event="advance_low_traffic"}`.
The bound applies to every canary, explicit or defaulted: holding a
zero-traffic stage longer produces no evidence.

After the final stage the release keeps its ADR-625 first-wake 5xx window,
which opens when the release first sees traffic. A low-traffic app is
therefore still protected when its traffic does arrive.

## Consequences

- A flag-free `gregale deploy` of a live app returns once the candidate is
  live at 1% and names the opt-outs; `gregale deployment wait --rollout`
  follows it. A healthy low-traffic release reaches 100% in roughly
  `3 × (2 + 5)` minutes on the balanced ladder instead of never.
- Each in-flight rollout uses the ADR-199 `RolloutConcurrencyGrant` (+1
  instance) for its duration. That cost already existed for `--safe`.
- `docs/capabilities.md` keeps progressive rollouts at `preview` until the
  drills below pass; this ADR changes the default, not the maturity claim.

## Rollout

Enable in staging first, then run and record, under `docs/drills/`:

1. a bad release (5xx and latency regression) aborts and restores the
   predecessor;
2. a low-traffic app reaches 100% through `advance_low_traffic`;
3. a defaulted deploy with meterd stopped falls back to an immediate cutover;
4. rollback recovery p95 ≤ 60 s (API-hosting scorecard).

Promote progressive rollouts toward `beta` in the product registry only with
that evidence, per the capability promotion rule.
