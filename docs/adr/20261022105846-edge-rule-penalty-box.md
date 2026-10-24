# ADR-20261022105846: Penalty box for edge rules

- **Status:** accepted
- **Date:** 2026-10-22
- **Related:** ADR-091 (edge rules), ADR-960 (log mode), ADR-831 (WAF),
  ADR-986 (protection recipes), ADR-961 (rule-set versions)

## Context

An edge rule judges each request on its own. A scanner that probes
`/wp-login.php`, `/.env` and `/.git/config` is refused each time, then
moves on to paths no rule covers. A client that exhausts a rate limit
waits a second and starts again. Vercel ("persistent actions") and
Cloudflare (rate-limit mitigation timeouts) let a rule keep refusing a
client for a while after it fired. We need the same, without new storage
on the request path.

## Decision

1. **A rule-level penalty.** A rule may carry
   `penalty: {seconds, after, within_seconds}`. Once it has refused the same
   client `after` times (default 1) within `within_seconds` (default 60),
   every request from that client to the app is refused for `seconds` (60
   to 86,400), whichever rule or path the request targets. The bounds live
   in `pkg/api/limits.go`.

2. **Which refusals count.** Only policy decisions count:
   - an IP denied, or outside an allow list (`kind=ip`);
   - a country denied, or outside an allow list (`kind=geo`);
   - a throttle bucket exhausted (`kind=throttle`);
   - a WAF block (`kind=waf` in block mode).

   A gate's own faults never box anyone (a forged forwarding header, a
   missing GeoIP database, an unavailable dependency). Challenge rules
   cannot carry a penalty: they refuse browsers that have not solved yet.
   A waf rule must be in block mode to keep one.

3. **The client.** The trusted client IP; for IPv6, its /64, which one host
   can rotate within.

4. **Where it is stored.** The penalty is part of the stored action
   (`edge_rules.action.penalty`), next to the kind payload, so it needs no
   migration and rule-set versions and rollback (ADR-961) carry it. The API
   exposes it as a top-level `penalty` on create, update and the rule
   response. PATCH keeps it across an action replacement, replaces it with
   `penalty`, and removes it with `clear_penalty`.

5. **The box is per gateway, in memory.**
   - Each `gatewayd-internal` (one per node) counts refusals per (app,
     rule, client) and holds boxes per (app, client).
   - It is bounded: 10,000 boxes per app (a full app drops its
     soonest-ending box) and 200,000 boxes and pending counts per gateway.
     Counting fails open when full.
   - A gateway restart releases its boxes, and a client spread across
     nodes is boxed on each node separately. Sharing boxes across the fleet
     is a later step.

6. **Enforcement.**
   - The box is checked before every other edge gate, so a boxed client
     costs one map lookup.
   - A boxed client gets 403 with code `penalty_box`, naming the rule, and
     `Retry-After` set to the seconds left.
   - Deployment smoke checks are never boxed.
   - A box applies only where its rule still applies with an enforced
     penalty. Removing the rule or its penalty, disabling it, letting it
     expire or putting it in log mode releases its clients on the next
     request.
   - A log-mode rule never refuses, so it never boxes.

7. **Signals.**
   - `gateway_edge_penalty_total{app,outcome}` with outcomes `boxed` and
     `rejected`.
   - Refusals also count in `gateway_edge_rejections_total{kind="penalty"}`,
     so `edge_rejection_pressure` sees them.
   - The edge-protection summary gains a `penalty` section, and `penalty`
     joins its gates.

8. **Adoption.** The CLI gains `--penalty-for`, `--penalty-after`,
   `--penalty-within` and, on update, `--clear-penalty`. The
   `block-scanner-paths` recipe takes an optional `penalty_minutes` (boxes
   on the first scanner hit) and `rate-limit-path` the same (boxes after
   three refusals within a minute).

## Consequences

- One busy NAT address shared by many people can be boxed by one of them.
  Thresholds above 1 and short penalties limit the damage. The 403 says
  when it ends.
- Penalties are invisible to the trace simulator, which evaluates one
  request with no history. It shows the rule as it is.

## Not in this step

- Sharing boxes across gateway nodes.
- Recording boxes as security events (ADR-964): it needs an online
  `outcome` CHECK widening.
- A list of boxed clients and a way to release one without editing the
  rule.
- Dry-run counts for log-mode rules.
