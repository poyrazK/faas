# ADR-956: Opt-in service wake-ahead along measured edges

- Status: Accepted
- Date: 2026-10-10
- Related: ADR-196, ADR-005, ADR-199
- Amends: ADR-196 (speculative wake-ahead deferred until measured evidence)

## Context

ADR-196 holds a cold internal call while its parked target restores. A fully
cold chain `public-api → auth → billing` therefore pays its restores one after
another. ADR-196 deliberately left out speculative wake-ahead along
`depends_on` edges because it "would admit instances for services a request may
never reach, spending the RAM ceiling on a prediction", and asked to revisit it
only with measured evidence. Waking the whole declared graph on first contact
stays rejected.

## Decision

An app may opt in (`PUT /v1/apps/{slug}/service-wake-ahead`,
`gregale wake-ahead <slug> on`). The setting lives in `app_service_wake_ahead`;
apid is its only writer and gatewayd-internal reads it through a 30 s cache.
It is off by default.

**Measured edges.** gatewayd-internal learns edges in memory. When an app
starts a cold wake (the public cold path, a service-mesh wake, or a
wake-ahead), it opens a 10 s follow window. A service call from that app inside
the window is a hit for the caller → target edge, and a benefit when the call
found the target parked, or warm only because a wake-ahead restored it (so a
working prediction does not erase its own evidence). Preview callers are not
learned from. Statistics halve every 200 caller wakes. The learner is bounded:
4,096 callers and 16 targets per caller.

**Wake-ahead.** When an opted-in app starts a cold wake, the gateway predicts
targets that have at least 20 observed caller wakes, were called on at least
half of them, and were parked (or pre-woken) on at least half of those calls.
It wakes at most three targets that have no healthy instance, at most once per
follow window each, in the background. A wake-ahead is an ordinary wake through
`Handler.ensureCapacity` and the WakeGate with the new trigger
`service.wake_ahead`: plan concurrency (§6.2-1), the RAM ceiling (§6.2-2),
admission queueing and the cold-boot fallback (ADR-005) all apply unchanged. A
request for the same target joins the restore already under way. Wake-ahead
cascades at most two levels.

**RAM guard.** No wake-ahead starts while fleet residency (billable RAM of live
instances over the active nodes' admission ceilings) is at or above 60%, read
from Postgres through the same cache. The guard runs before the wake, so a
refused prediction never becomes a wake error a real request could join, and
`service.wake_ahead` never triggers pressure parking (which only
`TriggerGateway` does), so a prediction never evicts another app.

**Measurement.** `gateway_service_wake_ahead_total{outcome}` counts `started`,
`used` (the caller called the target while the wake-ahead was fresh),
`unused`, `skipped_residency` and `failed`.

## Consequences

- A cold chain can restore in parallel instead of in sequence; the measured
  `used`/`unused` counters show whether that holds for real traffic.
- A woken-ahead service runs, and is billed (plan RAM + 8 MB per second, §4.7),
  from the moment it is woken. An unused one parks after its idle timeout. The
  customer opts in knowing this.
- Evidence is per gateway process and starts empty after a restart; in a
  multi-node fleet each gateway learns from the wakes and calls it sees.
- No scheduler change: the trigger is attribution only.

## Rejected alternatives

- **Waking the declared `depends_on` graph** — unchanged from ADR-196.
- **A scheduler-side refusal for speculative admissions.** WakeGate followers
  receive the leader's result, so a refused prediction would fail real
  requests that joined it. The guard therefore decides before any wake starts.
- **Durable edge statistics in Postgres.** The gateway does not write
  customer tables; in-memory evidence is enough to decide and bounded.
