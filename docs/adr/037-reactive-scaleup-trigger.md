# ADR-037 — reactive scale-up trigger (issue #169 / #172)

Status: Accepted, 2026-07-25. Owner: @poyrazK. Closes: #169, #172.
Related: #170 (PR #205, in flight, CPU signal source), #171 (preferential
reaper, separate PR).

- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.

## Context

The platform's autoscaling today is "warm pool + idle reaper": an instance
is admitted only when a request arrives with no routable target, and the
reaper parks it when its per-app idle timeout (60 / 60 / 300 / 600 s)
elapses. The plan's `max_concurrency` is a *ceiling*, not a target — to
get burst capacity, customers must pre-allocate `min_instances = N` and
pay for N resident instances at all times, even at idle.

#169 is the missing piece: a per-app, signal-driven scale-up trigger.
When measured load exceeds a configured target, schedd should admit
additional instances proactively — but only up to `max_concurrency`,
never beyond.

## Decision

Ship the **whole vertical slice** for #169 in one combined PR: schema
(issue #172), apid validation, schedd trigger, metrics, ADR, CLI, and
OpenAPI. The PR review boundary is the feature, not a single commit
landed piecemeal — splitting it across multiple PRs would leave the
"enabled" model in flight for weeks.

### Signal sources

Two parallel signal paths feed the trigger:

- **RPS** — scraped from gatewayd's local `/metrics` endpoint
  (`gateway_requests_total{app="…",code="…"}`). Parsed by
  `pkg/sched/scaleup.HTTPPromScraper` into a per-app cumulative count
  and folded into a 5-second ring buffer (1s buckets, 5 buckets).
  Per-instance RPS = `sum(window) / max(1, inflight_count)`.
- **CPU** — read from `pkg/sched/instancestats.Reader.SnapshotForApp`
  (PR #205, in flight on a feature branch). Pro/Scale only. The
  reader is nil-safe inside the trigger — a missing reader silently
  falls back to RPS-only mode.

### "Enabled" model — infer from target columns

There is no separate `enabled` boolean. An app has autoscale turned on
iff at least one of `autoscale_target_rps` / `autoscale_target_cpu_pct`
is non-NULL. Single source of truth: the targets themselves. This is a
deliberate choice over a separate boolean — the boolean would let the
two columns drift (an enabled app with both columns = 0 is a no-op
but a "valid" config to the user; the infer-from-targets model makes
the table unopinionated about enable state and pushes the decision
into a single `if app.AutoscaleTargetRPS == 0 && app.AutoscaleTargetCPUPct == 0 { skip }` check).

### Plan tiering

- **Free**: neither target. Single-concurrency plan; can't grow beyond
  one instance per app.
- **Hobby**: RPS only. The cost shape of "scale on CPU without a
  `min_instances` floor" is unbounded on the cheaper tiers.
- **Pro**: RPS + CPU. The full surface.
- **Scale**: RPS + CPU. Same as Pro; Scale already has the largest
  caps so the same fields apply unchanged.

The plan gate runs in apid's `validateUpdateApp` BEFORE the bounds
check so a Free account PATCHing a 50-rps value surfaces the gate
(`403 plan_autoscale_not_allowed`), not the bounds
(`422 invalid_autoscale_target_rps`). 403 supersedes 422.

### Tick rate

1 second. This balances two competing costs:
- "Admit Nth instance before the gateway queue builds" — 1s is fast
  enough that the per-instance RPS exceeds the target for at most
  one tick before the trigger fires.
- "Don't hammer Postgres with a full app list on every tick" —
  `ListAllApps` is one query, and the trigger already uses
  `Ledger.Concurrency` (in-memory) for the divisor.

`api.ScaleUpDecisionIntervalSeconds = 1` is the single source of truth.

### Ledger interaction

Read-only. The trigger never bypasses `NodeLedger.Admit`; it calls
`Engine.AdmitInstance`, which goes through the same ledger path the
gateway uses on a request-driven wake. The cap is enforced inside
`AdmitInstance` (per-app `perApp[appID] < maxConc` check at
admission.go:129), so a 6th wake on a 5-instance app returns
`WakeResult{AtCapacity: true}` even when the trigger decided to
admit. The trigger observes this as `OutcomeRejectAtCap`, NOT
`OutcomeAdmit`, and never writes a FAILED row.

### Metrics

- `schedd_scale_up_decisions_total{app, outcome}` — counter, closed
  label set `{admit, reject_at_cap, no_signal}`,
  pre-instantiated so the rows surface in `/metrics` from boot.
- `schedd_scale_up_admit_rps` — unlabelled histogram, per-instance
  RPS at the moment of admit. Sized to the realistic per-instance
  range (1..1000).

`app` cardinality is bounded by the number of apps with autoscale
configured (the platform caps at hundreds of apps per account, only
a fraction of which enable autoscale).

### Nil-safety matrix

- `appStore == nil` → no-op (boot, before store wire-up).
- `instats.Reader == nil` → CPU path skipped, RPS-only mode
  (PR #205 not yet merged is fine).
- `PromScraper == nil` → RPS path skipped; degraded mode logs once
  and emits no_signal.
- `engine == nil` → no-op (boot).
- `Loop.WithScaleUp(nil)` → the ticker arm of `Run`'s select never
  fires. schedd's test surface stays green without a trigger.

The trigger is constructed defensively at every layer so schedd can
wire it before every downstream dependency is fully online.

### Why a separate package, not `pkg/sched/loop.go` directly

The trigger has its own inputs (instancestats + a request-count ring
buffer), its own worker struct, and its own metrics — three
responsibilities that don't fit `loop.go`'s ticker-arm pattern
cleanly. The package mirrors the `pkg/sched/heartbeat` /
`pkg/sched/instancestats` shape (constructor → `Tick(ctx)` → `Run(ctx)`).

The `scaleup.AdmitResult` type intentionally avoids importing
`pkg/sched` (which would create an import cycle: `sched` → `scaleup` →
`sched` via `loop.go`). The cmd/schedd wiring includes a thin
`s schedScaleUpEngine` adapter that lifts the relevant fields from
`sched.WakeResult` into `scaleup.AdmitResult`. The trigger only
inspects `AtCapacity` + `InstanceID` — the rest of `WakeResult` is
internal to the gateway's wake path.

## Consequences

- **Positive**: a customer on Hobby+ can configure a per-instance
  RPS target and stop paying for `min_instances` to handle burst
  traffic. The trigger admits extra instances on the fly, the
  reaper parks them when the burst subsides, and the customer
  pays only for the time those instances were actually resident.
- **Positive**: dashboard panel for "scale-up aggressiveness" via
  `schedd_scale_up_admit_rps` p95/p99 (spec §12).
- **Positive**: PR #171 (preferential reaper) can now read the
  trigger's per-app RPS history to bias eviction order — the
  trigger's ring buffer is the input side, the reaper is the
  output side.
- **Negative**: the trigger's correctness depends on
  `instancestats.Reader` landing (PR #205). Until that merges, the
  CPU target is inert and Hobby/Pro+Scale are functionally identical
  on CPU. We accept this — the trigger is correct in RPS-only mode
  and PR #205 is on a feature branch we can land first.
- **Negative**: the trigger reads every app per tick. With ~100 apps
  on a single box, that's 100 list + 100 ledger lookups per second.
  Within the 85% RAM ceiling budget but worth watching in
  benchmark (out of scope for this PR; spec §14 acceptance gate).

## Reconciliation note (2026-07-28, issue #172)

Closing #172 against the shipped implementation requires recording three
deviations between the issue text and what landed in PR #229.

1. **Field naming.** The issue proposed
   `autoscale_target_rps_per_instance` / `autoscale_target_cpu_per_instance`.
   PR #229 shipped `autoscale_target_rps` / `autoscale_target_cpu_pct`.
   Rationale for the shorter names:
   - "Per instance" is implicit. The autoscale target column is one
     row per app — there is no fleet-wide alternative in v1. The
     `_per_instance` suffix would add 13 characters to every
     `faas apps update --autoscale-target-rps …` invocation without
     disambiguating anything.
   - The `pct` suffix on CPU prevents the unit ambiguity: a `0..100`
     percentage and a raw CPU-fraction are easy to mix up; the
     column name carries the unit.
   - Both are public on `apps` (GET response) and on the wire
     (PATCH body). Renaming later would be a breaking API change;
     keeping them short now is the cheaper call.
   **Decision: keep the shipped names.** No rename.

2. **Plan tiering.** The issue proposed Pro+ for
   `autoscale_target_rps` and Scale for `autoscale_target_cpu_pct`.
   PR #229 shipped Hobby+ for RPS and Pro+ for CPU.
   - Hobby+ for RPS: Hobby is the first paid tier (5 apps, 2
     concurrency). The RPS target is the lowest-cost way to handle
     Hobby's burst shape, and Hobby customers already pay €9/mo —
     locking the RPS target behind Pro would force them to either
     over-provision `min_instances` (Pro/Scale-only feature) or
     upgrade two plans just to get burst capacity.
   - Pro+ for CPU: CPU-driven scale-up on Hobby is unbounded
     (no `min_instances` floor, Hobby customers pay only for
     resident time). Tightening this to Pro+ matches the
     `MinInstancesAllowed` gate which is also Pro+ — same
     economic reasoning.
   - The Scale plan gets the same gates as Pro — Scale already
     has the largest caps, no new surface.
   **Decision: re-tier Hobby's RPS gate to `false` (Pro+) — see
   Amendment below.** The shipped Hobby+ behavior was a deliberate
   first cut; this PR aligns it with the issue's spec while
   preserving the economic rationale for keeping CPU at Pro+.

3. **`autoscale_min` / `autoscale_max` columns (issue §"API").**
   The issue proposed adding two columns: `autoscale_min`
   (default 0) and `autoscale_max` (default = current
   `max_concurrency`). PR #229 shipped neither. Rationale:
   - The floor already exists as `min_instances` (Pro/Scale,
     `ux_spec §6.5`). Adding `autoscale_min` would create two
     sources of truth for the same thing.
   - The ceiling already exists as `plan.MaxConcurrency`, the
     hard cap `pkg/sched/admission.go:129` enforces inside
     `AdmitInstance`. The trigger uses `MaxConcurrency` directly
     (`pkg/sched/scaleup/trigger.go:157`, `Headroom =
     MaxConcurrency - Concurrency`). Adding `autoscale_max`
     would be a redundant ceiling: the trigger never overrides
     `MaxConcurrency`, so the column would either be redundant
     (== MaxConcurrency) or a soft-target that the cap ignores.
   **Decision: do not add these columns.** The shipped
   semantics are exactly what `autoscale_min = min_instances`
   and `autoscale_max = plan.MaxConcurrency` would express, with
   no double-bookkeeping.

Issue #172 is closable as "shipped via PR #229 with the three
deviations above documented in this ADR; Hobby→Pro re-tier applied
in the same PR per the Amendment below."

## Amendment (2026-07-28): re-tier ScaleUpTargetRPSAllowed to Pro+

Re-tier Hobby's RPS-target gate from `true` to `false`, matching
issue #172's text ("Pro+ for target_rps"). Hobby customers who
want RPS-driven burst must upgrade to Pro.

**Trade-off.** The Hobby plan loses its lowest-friction burst
mechanism. Hobby customers currently have two knobs for absorbing
burst: (a) over-provision `min_instances` (but that's also
Pro+-gated — Hobby can't touch it), or (b) upgrade to Pro. After
this amendment, Hobby customers have exactly one knob: upgrade.
The upsell-pressure rationale: Hobby at €9/mo is already cheap,
and the platform-wide observation that "Hobby customers who hit
RPS burst are exactly the Hobby customers ready for Pro" has held
across the first 60 days.

**Rejected alternative.** Lock CPU behind Scale (issue asked for
Scale-only on CPU). Rejected because the CPU gate is already
Pro+-gated and tightening it would require a separate re-tier
with no matching revenue justification — Hobby already can't
use CPU today; we're not closing a door Hobby had access to.

**Cutover.** Zero production rows today (`apps.autoscale_target_rps`
shipped 2026-07-25 via PR #229). Any Hobby account that PATCHed a
target before this PR will silently see their setting ignored
after this PR lands. The PR description carries the cutover
language; no schema rewrite needed (the column is nullable, the
trigger's "infer from target" model treats `target_rps > 0` as
"enabled" regardless of plan).

## Open follow-ups (not blocking)

- **Hobby CPU target** — defer; the current tiering (Hobby = RPS-only)
  is the conservative default. A future `hobby_cpu_target_allowed bool`
  opt-in could ship behind a flag.
- **Per-instance vs fleet-wide RPS target** — current draft is
  per-instance. Fleet-wide is a one-line change to `decide()` and a
  UX call; defer.
- **Reconcile with PR #170 / #171** — once those land, this ADR
  should add a "Related work" section referencing the merge order.
