# ADR-950 · Opt-in depends_on wake-ahead

- **Status:** accepted
- **Date:** 2026-10-10
- **Related:** ADR-196, ADR-098, ADR-219, ADR-266, ADR-288

## Context

ADR-196 made a parked internal service wake on demand: a call to
`<slug>.svc.gregale` is held at the node-local proxy while the snapshot
restores. It recorded the remaining cost explicitly:

> A chain of cold services serialises their restores: `public-api → auth →
> billing` pays three sequential wakes on a fully cold path. Speculative
> wake-ahead along declared `depends_on` edges is deliberately **not** part
> of this ADR — it would admit instances for services a request may never
> reach, spending the RAM ceiling on a prediction.

The serial cost is real. A restore is not on the order of the 350 ms
reference budget in production today: on 2026-10-09 the measured hit-path
restore from admission to first byte was p50 555 ms on the GCP compute nodes.
Three cold hops therefore cost about 1.7 s before the first handler runs, and
the only workaround is `min_instances` on every internal dependency — exactly
the always-on cost scale-to-zero exists to remove.

ADR-196's objection is about who pays for the prediction. The platform should
not guess; the customer knows whether `public-api` always calls `auth`.

## Decision

1. **Caller opt-in.** A Compose workload declares
   `x-gregale-service-wake-ahead: declared`. It is persisted as
   `apps.manifest.service_wake_ahead` (no migration; the manifest is JSONB) and
   returned as `service_wake_ahead` on the app and plan workload. It is
   source-owned: removing the line turns it off. Empty and unknown values are
   `off`, so an older gateway never speculates on a setting it does not
   understand.
2. **Trigger point.** When the gateway's `WakeGate` leader is about to admit
   an app (`Handler.coldStart`) for a request-driven wake — trigger `gateway`,
   `service.mesh`, or `service.wake_ahead` — it starts wake-ahead for that
   app and returns immediately. Floors, crons, prewarm, and rollout wakes do
   not qualify: nothing is waiting on their callees.
3. **Plan.** Off the critical path, the planner loads the caller and, if it
   opted in, walks `manifest.service_bindings`. Each binding is resolved with
   the service proxy's resolver (so PR-preview and scenario-test scoping is
   identical to a real call) and authorized with the proxy's authorizer
   (same-account, declared-binding, preview, and caller policy). A binding
   the caller could not call is never restored. At most
   `ServiceWakeAheadMaxTargets` (8) targets are restored per caller wake.
4. **Restore.** Each target goes through `ensureCapacity` with the new trigger
   `service.wake_ahead`. Because the `WakeGate` is keyed by app, a real call
   that arrives during the restore joins it, and a restore already in flight
   absorbs the wake-ahead. Wake-ahead therefore never creates an instance that
   a real call would not have created; it only creates it earlier. A
   dependency's own restore is itself a leader wake with trigger
   `service.wake_ahead`, which carries the chain one hop further. Cycles end
   because an app already waking only gains a waiter.
5. **Bounds.** One gateway process runs at most
   `ServiceWakeAheadInflightPerNodeMax` (64) planning and restore goroutines.
   When the cap is reached, work is skipped, never queued. Each restore is
   bounded by `ServiceWakeAheadTimeout` (30 s, the wake hold). schedd remains
   the only admission authority: plan concurrency, the RAM ceiling, and
   security quarantine apply exactly as for any other wake.
6. **Operator switch.** `FAAS_GATEWAY_SERVICE_WAKE_AHEAD` in
   gatewayd-internal. The drop-in ships it on (customers still opt in); off
   restores ADR-196 behaviour on that node without a deploy.
7. **Evidence.** `gateway_service_wake_ahead_total{outcome}` counts restored,
   already_warm, at_capacity, wake_failed, denied, unresolved, plan_failed,
   saturated, and truncated decisions. Per-app attribution lives in the wake
   timeline under trigger `service.wake_ahead`; whether a speculative instance
   served traffic before parking is visible there and in
   `gateway_service_wake_latency_seconds`, which should fall for opted-in
   chains.

## Consequences

- An opted-in cold chain pays roughly one restore instead of one per hop.
- A wasted prediction costs the customer what any wake costs: plan RAM + 8 MB
  per running second until the normal idle timeout parks it (§4.7 billing is
  unchanged). The customer chose that trade by opting in.
- Wake-ahead waiters count against the target app's wake queue like any
  waiter, and schedd may refuse them at the RAM ceiling; both are reported,
  neither delays the caller's own restore.
- Exact-deployment (rollout cohort) and dev-bridge routing are not predicted:
  wake-ahead restores the app's default target, and a keyed call still wakes
  its exact cohort on demand.

## Rejected alternatives

- **Default on for every declared edge.** This is what ADR-196 rejected. An
  edge such as `api → reports` may be exercised by 1% of requests; restoring
  `reports` on every `api` wake spends the RAM ceiling on a guess.
- **Learned edge probabilities.** Correlating a dependency's wake with its
  caller's wake needs cross-node state: the caller's outbound calls traverse
  the proxy on the caller's node, which is not necessarily the gateway that
  admitted the caller. It can be layered on later as an `auto` mode using
  the evidence above, without changing the opt-in contract.
- **A shorter idle timeout for speculative instances.** It would need schedd
  to track why an instance exists for its whole life, and the idle path is
  already the plan's contract. Deferred until the counters show waste.
- **Restore dependencies after the caller is admitted.** Admission returns
  when the caller is ready, so that would still serialise the chain.
