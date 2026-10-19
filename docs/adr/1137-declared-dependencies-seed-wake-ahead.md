# ADR-1137: Declared dependencies seed opt-in wake-ahead

- Status: Accepted
- Date: 2026-10-19
- Related: ADR-196, ADR-977, ADR-1132, ADR-1134
- Amends: ADR-977 (what an opted-in app wakes before the learner has evidence)

## Context

ADR-977 lets an app opt in to wake-ahead: when it starts a cold wake, the
gateway restores the services it measured the app calling, so a cold chain
`public-api → auth → billing` restores in parallel instead of one after
another. A prediction needs at least 20 observed caller wakes. Until then an
opted-in app gets nothing, which is exactly when a new or rarely woken service
graph pays its sequential restores, and every gateway learns separately.

The app already says what it calls: `depends_on` becomes the declared service
bindings that the gateway enforces (the `declared` caller policy). ADR-196 and
ADR-977 rejected waking that whole declared graph on first contact, because it
admits instances for services a request may never reach and spends the RAM
ceiling on a prediction. That stays rejected for apps that have not opted in.

## Decision

For an app that opted in to wake-ahead, its declared dependencies stand in for
the evidence the learner does not have yet.

- **Before evidence.** While the gateway has fewer than
  `ServiceWakeAheadMinWakes` (20) wakes of the caller, the prediction is the
  caller's declared dependencies, in declaration order, at most
  `ServiceWakeAheadMaxTargets` (3).
- **After evidence.** From the 20th wake the measured rule of ADR-977 alone
  decides, for declared and undeclared targets alike. A declared dependency
  the caller rarely calls after waking, or that is usually warm already, stops
  being woken; a declared dependency the caller does call keeps qualifying,
  because a call that finds a woken-ahead target warm still counts as a
  benefit. The learner keeps measuring while the prior is in use, so the
  evidence that ends it is the evidence ADR-196 asked for.
- **Resolution.** gatewayd-internal resolves each declared name with the
  service proxy's own resolver and authorizer, exactly as a call from the
  caller would be. A name that does not resolve, the caller itself, a target
  the caller may not call (account, allowed callers, preview policy) and a
  repeated target are skipped. Preview and scenario-test callers declare
  nothing, as the learner does not learn from them. The list is cached for
  `ServiceWakeAheadCacheTTL` (30 s) per caller; a failed read declares
  nothing.
- **Everything else is unchanged.** The opt-in (off by default), the 3-target
  cap, the 2-level cascade (a woken-ahead dependency wakes its own declared or
  measured dependencies only if it opted in too), the 60% fleet residency
  guard, the wake trigger `service.wake_ahead`, admission and billing all stay
  as ADR-977 specifies. The `started`, `used`, `unused`, `skipped_residency`
  and `failed` counters include these wake-aheads.

## Consequences

- An opted-in app restores its declared dependencies from its first cold wake
  instead of after 20.
- During that period a declared dependency can be woken and not called. Each
  such restore is billed like any wake (plan RAM + 8 MB per second until its
  idle timeout parks it) and shows as `unused`. The cost is bounded by three
  targets per wake, two levels, and the 20 wakes after which evidence decides;
  ADR-1132 keeps that evidence across gateway restarts, so the prior does not
  return on every deploy.
- A caller with an environment-scoped route still has its target woken by app,
  as with measured wake-ahead.

## Rejected alternatives

- **Waking declared dependencies by default.** This would change what every
  app with `depends_on` pays without it asking. It needs the `used` share this
  decision produces first.
- **Shortening the learning period for every edge.** Fewer wakes judge
  undeclared edges on less evidence; the declaration is the stronger signal.
- **Keeping declared targets after evidence rejects them.** The declaration
  says a call is possible, not that it follows a wake; measured evidence is
  the better judge once it exists.
