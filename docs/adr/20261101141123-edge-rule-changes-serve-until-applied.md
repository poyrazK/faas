# ADR: Hosts keep serving while an edge-rule change converges

Date: 2026-11-01
Status: Accepted (owner, 2026-11-01)
Amends: [ADR-570](570-traffic-enforcement-guarantees.md) ("Ordinary policy updates fence new requests during convergence"; the `FAAS_TRAFFIC_AUTHORITATIVE_HOST_POLICY` off bullet), [ADR-091](091-edge-rules.md) (two-phase convergence, amendments 2026-10-27 and 2026-10-30)

## Context

Every edge-rule change runs a two-phase barrier: apid publishes `prepare`,
waits for every live gateway's acknowledgement, commits, publishes `apply`
and waits again. Between a gateway's prepare and its apply, that gateway
answered every request to the covered hosts with 503 `capacity_unavailable`
("Edge policy update in progress", `Retry-After: 1`). In authoritative host
policy mode the public route graph pin refused as well.

The window is the whole barrier: two NOTIFY round trips plus the commit.
On production the CLI's rule changes took 0.7–1.9 s end to end, and the
covered pattern is usually the app's host or `@app`. So every rule edit
took the app down for about a second. A GitOps sync, a rollback or attack
mode doing several edits could take it down for longer. Customers saw
`capacity_unavailable`, the same code as real capacity exhaustion.

Most rules match `/**`, so scoping fences by path would not shrink this.

The fence held the change's linearization point at commit: after commit,
no new request ran under the old rules on any gateway. Customers cannot
observe that point. What they can observe is the API returning, and the
apply barrier already guarantees that no gateway serves the old rules after
that.

## Decision

1. **A pending change does not refuse requests.** Between prepare and
   apply, a gateway serves the covered hosts under the rules it has: a
   cached entry or a fresh read. Its apply drops the cached entries, as
   before. Each request still pins one sealed copy of its host's policy
   (one snapshot read in authoritative mode), so a request runs entirely
   under the old rules or entirely under the new ones. During the window,
   different requests and different gateways may disagree.
2. **The API's guarantee is unchanged.** A change returns after every live
   gateway's apply acknowledgement, and from then on no gateway serves the
   old rules. A prepare that a live gateway does not acknowledge still
   refuses the change before anything is written. A missed apply is still
   reported, and the gateway catches up through the notification resync
   or the 30 s rule cache TTL.
3. **One case stays fail-closed: a route target that stops resolving.** A
   `route` rule names its target by slug. An app rename commits the rename
   before the retargeted rules apply. Meanwhile a cached rule can name a
   slug that no longer resolves, and the cached path would serve the routed
   path from the source app. While a change to the source host is pending,
   such a request now answers 503 `traffic_policy_unavailable` with
   `Retry-After: 1`. Authoritative mode already refused an unresolved
   target.
4. **Fences stay as bookkeeping.** Gateways still record a fence per
   prepare: owner-scoped, ended by apply or abort, with a 10 s TTL. Fences
   feed `gateway_edge_rule_converging_hosts`,
   `gateway_edge_rule_generation_lag` and `FaasEdgeRuleConvergenceStuck`,
   and they answer the route-target check in decision 3. Metric names are
   unchanged.
5. **Release order.** The change is gateway-only. A previous-release gateway
   beside a new one keeps fencing with 503 until it is replaced, so mixed
   fleets behave as each gateway's release does. apid is unchanged and
   still sends prepare.

## Consequences

- A rule edit no longer 503s the app's hosts. A tightening change, such as
  a new IP deny or auth gate, takes effect at each gateway's apply instead
  of at commit. The difference is at most the barrier, and it ends before
  the API returns.
- The CLI's `EdgeRuleChangeConverging` hint is about apid's
  `X-Faas-Edge-Rules-State: converging` (a committed change with
  incomplete applies) and is unchanged.
- `docs/edge-rules.md` ("Changing rules"), the `FaasEdgeRuleConvergence`
  runbook and the alert descriptions now describe serving during a change.

## Rejected alternatives

- **Path-scoped fences.** Nearly every rule matches `/**`, so they would
  hold the same requests.
- **Hold requests until apply instead of refusing.** This keeps commit-time
  linearization but stalls every request to the app for the barrier,
  about 1–2 s and up to the 10 s TTL when an apply is lost. It buys
  nothing a customer can observe.
- **Drop the prepare phase.** Serving no longer needs it, but it is what
  refuses a change before commit when a live gateway cannot take it.
  Without it, a change would commit and then report incomplete
  convergence.
