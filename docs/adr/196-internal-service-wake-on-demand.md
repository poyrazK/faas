# ADR-196 · Internal service wake-on-demand

- **Status:** accepted
- **Date:** 2026-09-21

## Context

ADR-167 through ADR-170 gave workloads a complete internal service mesh: a
cross-VM endpoint registry projected from the gateway's routing cache, a
node-local HTTP proxy with source-IP caller identity, and a portable
`<slug>.svc.gregale` name answered by a per-node DNS resolver. Combined with
`apps.visibility = 'internal'` (which removes an app from every public
route-resolution branch in `cmd/gatewayd-internal/backend.go`), a customer can
express the shape this platform is meant to make easy:

```
public-api  ──►  auth
            ├─►  billing
            └─►  recommendation
```

with no VPC, subnet, security group, internal load balancer, service-discovery
registry, or DNS record anywhere in their configuration.

One thing was missing, and it undercut the whole arrangement.
`ServiceProxy.ServeHTTP` read the endpoint registry and, when the target had
no running replica, answered:

```
503  "service has no healthy replicas"
```

`PGBackend.ServiceEndpoints` reconciles from **live** targets only; neither it
nor the proxy has ever had a path to `schedd`. So the internal mesh could
route to a service that happened to be awake, and could do nothing at all with
one that was parked.

The consequence is not a rough edge, it is an inversion of the product. The
public edge holds a request during a restore (spec §4.1 wake-blocking, the
`WakeGate`'s 512/30 s queue); the internal path did not. An internal service —
`auth`, `billing`, a recommendation model — is idle far more of the time than
a public endpoint, so it is exactly the workload scale-to-zero should pay off
on hardest. Instead the only way to make internal calls reliable was to pin
`min_instances >= 1` on every dependency, converting each one into permanently
resident RAM charged against the 47,600 MB admission ceiling. Gregale's
central economic claim — "a parked app consumes zero resident RAM"
(invariant §6.2-4) — held for the topology customers were least likely to
deploy, and failed for the one this feature exists to enable.

## Decision

A call to a parked internal service **holds and wakes**, exactly like a public
request.

When the endpoint registry returns no routable replica, `ServiceProxy` invokes
an optional `ServiceProxyWaker` seam, invalidates the cached registry lease for
that app, and re-reads the registry before answering. Production wires that
seam to `Handler.EnsureServiceCapacity`, which calls the same
`Handler.ensureCapacity` the public edge uses.

Reusing the handler's admission path rather than giving the proxy its own is
the substance of this decision, for three reasons:

1. **`WakeGate` is keyed on `appID` alone.** A public request and an internal
   service call arriving for the same parked app therefore coalesce into one
   restore instead of racing to admit two instances. A second, parallel
   admission path would have reintroduced exactly the wake fan-out defect that
   was reverted in production in PR #1300 → #1301.
2. **Invariant §6.2-1 stays intact across both entry points.** The ≤
   `max_concurrency` bound over {WAKING, COLD_BOOTING, RUNNING} is enforced by
   `Backend.Admit`'s serialized HealthyCount-then-cache-update; routing the
   internal path through the same call inherits that guarantee rather than
   restating it.
3. **Per-app wake policy already exists.** `concurrencyConfigForApp` resolves
   the plan's waiter cap, wait budget, and overflow posture from the target
   app. The internal path gets the customer's configured behaviour for free.

### Wake trigger

Internal wakes stamp `sched.TriggerServiceMesh = "service.mesh"` rather than
`TriggerGateway`. The waiter is a peer workload, not an Internet client, and an
operator attributing cold-start latency or wake cost needs to tell internal
fan-out apart from customer traffic in the wake timeline. Threading a
`trigger` parameter through `ensureCapacity` / `coldStart` (two pre-existing
callers, both passing `TriggerGateway`) is the mechanical cost of that
distinction.

### Registry lease invalidation

The 5 s endpoint lease exists to keep the hot path off Postgres. The wake has
just changed precisely the state that lease caches, so the post-wake read
bypasses it. Without the invalidation every cold internal call would 503
regardless of how fast the restore completed — the proxy would re-serve the
empty snapshot it captured moments earlier. This is pinned by
`TestServiceProxyInvalidatesEndpointLeaseAfterWake`.

### Failure semantics

| Outcome | Response |
|---|---|
| Wake succeeds, replica routable | Forwarded normally |
| Wake queue full (`*WakeQueueFullError`) | 503 + `Retry-After`, mirroring the public edge |
| Admission failure (no headroom, scheduler unreachable) | 503 `service could not be woken` |
| Wake succeeds but nothing routable (at plan concurrency ceiling) | 503 `service has no healthy replicas` (unchanged wording) |
| Target app deleted between resolution and wake | 503 `service has no healthy replicas`, not an error |
| No waker wired (tests, dev without schedd) | 503 `service has no healthy replicas` — pre-ADR-196 behaviour |

`Retry-After` is floored at one second: a sub-second budget rendered as `0`
reads to clients as "retry immediately" and turns a restoring dependency into
a hot loop.

## Consequences

- Internal services genuinely scale to zero. `min_instances` becomes a latency
  choice rather than a correctness requirement, and a parked dependency stops
  consuming resident RAM against the tenant budget.
- A cold internal call now pays snapshot-restore latency instead of failing.
  Callers should set client timeouts above the platform wake budget (§6.3,
  p95 < 350 ms on the reference node) plus their own handler time.
- A chain of cold services serialises their restores: `public-api → auth →
  billing` pays three sequential wakes on a fully cold path. Speculative
  wake-ahead along declared `depends_on` edges is deliberately **not** part of
  this ADR — it would admit instances for services a request may never reach,
  spending the RAM ceiling on a prediction. Revisit only with measured
  evidence from `service.mesh` wake-timeline data.
- The waker is an optional seam, so any wiring without a scheduler (unit
  tests, single-box dev) keeps the previous fail-fast behaviour rather than
  failing closed on a nil dependency.

## Rejected alternatives

- **Wake asynchronously and fail the triggering call.** Simpler and bounds
  caller latency, but every cold internal call fails once and pushes retry
  logic onto the customer — the precise Kubernetes-shaped burden this feature
  exists to remove.
- **A dedicated admission path inside `ServiceProxy`.** Would duplicate the
  gate, the plan policy, and the fan-out guard, and would let a public request
  and an internal call race to admit two instances for one parked app.
- **Per-app configuration of hold-vs-fail.** Another entry in
  `pkg/api/limits.go` for a decision customers should not have to make; the
  public edge does not offer it either.
- **Waking the whole `depends_on` graph on first contact.** See consequences
  above: it spends the admission ceiling on a prediction.
