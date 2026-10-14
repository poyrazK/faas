# ADR-1134: Wake-ahead learns private TCP edges

- **Status:** proposed
- **Date:** 2026-10-13
- **Amends:** ADR-977 (which service calls teach the wake-ahead learner)
- **Related:** ADR-576 (private TCP service addressing), ADR-196, ADR-1131

## Context

ADR-977 restores a caller's measured service dependencies while the caller
wakes. Its learner is fed only by the HTTP service proxy. The most common
microservice shape, an app and the database, cache or queue it reaches over
private TCP (`<slug>.svc.gregale:5432`), never teaches it: a cold app still
connects, then waits for the database to restore, then serves. The TCP proxy
already holds such a session while it wakes the target (ADR-576), and since
ADR-1131 it knows which sessions had to wake it, but that never reached the
learner.

## Decision

The service TCP proxy feeds the same learner. An authorized session is
observed once, as the TCP proxy hands it to the first replica it picked,
with whether the session had to wake the target. That is the moment the HTTP
proxy observes a call. Observing at session end would be wrong for TCP: a
pooled database connection can stay open for hours, far past the caller's
10 s follow window.

Everything else is ADR-977 unchanged. The same evidence bar applies (≥ 20
caller wakes, the target used after half of them and parked on half of
those). So do the per-app opt-in, the three-target cap, two-level cascade,
fleet residency guard, trigger and outcome metrics. The learner counts an
edge once per caller wake however many calls or connections it saw, so a
connection pool opening several sessions after a wake is one observation of
the edge. Refused sessions (identity, authorization, undeclared port, plan
session limit) and PR preview callers are not observed, as on HTTP.

The service map's wake-ahead suggestion (ADR-1131) counts cold sessions
together with cold calls, against calls plus sessions, because both now teach
the learner.

## Consequences

- A cold app that connects to a parked internal database within 10 s of
  waking, after at least 20 such wakes, gets the database restored alongside
  itself once its owner opts in. The connection then waits for the slower of
  the two restores instead of their sum.
- A session that lands on a just-stopped replica is observed as warm. The
  TCP proxy's single retry then wakes the target after the observation. This
  happens only inside the 5 s endpoint lease after a park or drain, and the
  HTTP path behaves the same way.
- No new metric, limit or setting. Wake-ahead started by a TCP edge shows up
  in the existing `gateway_service_wake_ahead_total` and
  `gateway_service_wake_ahead_edge_total` series.

## Validation

`TestPrivateTCPWakeAheadMetal` deploys an internal TCP echo service and a
caller from images. Each cycle parks both, then wakes the caller with a
request that opens a TCP session to the service. Twenty-odd cycles teach the
edge. The opt-in is then switched on and off in alternating blocks of four
cycles, on one gateway in one run. Restores on the shared n2 host varied
about 3x with host load between runs an hour apart, so comparing separate
runs or phases measured the host, not wake-ahead.

On n2 (2026-10-14), the cold caller → TCP service round trip was:

- wake-ahead off: p50 3.32 s, p95 3.73 s (n=8);
- wake-ahead on: p50 2.34 s, p95 3.64 s (n=8);
- 8 wake-aheads started and all 8 were used.

With wake-ahead on, the service's restore finished within 0.06–0.36 s of the
caller's. Without it, the service's restore finished 0.7–2.1 s after the
caller's. On this nested-KVM host two concurrent restores each run slower
than one alone, which is why p95 barely moves.

## Rejected alternatives

- **Observe when the guest dial is confirmed.** It would be exact for the
  just-stopped replica case, but needs a callback threaded through the
  forwarder or a context value for a rare case the HTTP path accepts too.
- **A separate TCP learner or threshold.** The question is the same as for
  HTTP: does this caller's wake predict a cold dependency. A second learner
  would split the evidence for apps that use both.
