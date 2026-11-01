# FaasEdgeRuleConvergence

Alerts (warn, family `edge_rules`), in
`deploy/ansible/roles/prometheus/files/faas.rules.yml`:

- `FaasEdgeRuleListenerStale`: a gateway stopped stamping its edge-rule
  listener heartbeat.
- `FaasEdgeRuleConvergenceFailing`: apid's edge-rule changes are not
  converging.
- `FaasEdgeRuleConvergenceStuck`: a gateway has had hosts with prepared,
  unapplied edge-rule changes for 10 minutes.

Decision: ADR-091 amendments (2026-10-27); ADR-20261101141123 (hosts keep
serving during a change).

## How a rule change converges

Every edge-rule change (API, dashboard, GitOps, attack mode, rollback) runs
a two-phase barrier:

1. **prepare**: apid publishes `edge_rule_changed` with phase `prepare`.
   Each live gateway records a fence on the hosts the change covers and
   acknowledges. The hosts keep serving their current rules; a fence only
   makes a `route` rule whose target no longer resolves answer 503
   `traffic_policy_unavailable` instead of falling back to the source app
   (an app rename). apid waits up to 3 s for every gateway; if one does not
   answer, the change is refused with 503 and nothing is written.
2. **commit**: apid writes the change.
3. **apply**: apid publishes phase `apply`. Each gateway drops the hosts'
   cached rules, lifts the fence and acknowledges. apid answers the
   customer only after every gateway's apply acknowledgement.

A fence expires 10 s after its prepare even if no apply arrives.

Each gateway stamps `compute_nodes.edge_listener_heartbeat_at` every 10 s
from the loop that receives these notifications. apid leaves a gateway out
of the barrier once its stamp is 30 s old
(`apid_edge_rule_convergence_skipped_gateways_total{node}`, and the apid log
line `edge-rule convergence skips a gateway whose listener is stale`). A
left-out gateway still serves traffic; it picks up changes only as each
host's 30 s rule cache entry expires.

## FaasEdgeRuleListenerStale

The gateway's notification loop is not running, or cannot write to
Postgres.

1. Is the gateway process up and serving? Check `up{job="gatewayd-internal"}`
   and its request rate. A dead process fires its own alerts; this one means
   the process is alive but its listener is not.
2. Read its logs for `gatewayd: edge listener heartbeat` (the stamp write
   failed) and `gatewayd: subscribe invalidations` (the listener never
   started).
3. Compare the stamps:

   ```sql
   SELECT name, role, edge_listener_heartbeat_at, now() - edge_listener_heartbeat_at AS age
     FROM compute_nodes
    WHERE active AND gateway_target_url IS NOT NULL
    ORDER BY age DESC NULLS LAST;
   ```

**Mitigation**: if Postgres is fine and only this gateway's stamp is stale,
restart its gatewayd-internal. Until the listener recovers it serves rules
up to 30 s old, and it also misses route and app invalidations, so treat a
long outage as a reason to drain the node.

## FaasEdgeRuleConvergenceFailing

- **`phase="prepare"`**: customers' rule edits fail with 503 ("edge-rule
  fleet convergence is unavailable"). A gateway that is live (stamp fresh)
  is not acknowledging within 3 s: its listener is slow, Postgres
  `NOTIFY` delivery is delayed, or apid cannot subscribe to
  `edge_rule_ack`. apid logs `edge-rule convergence prepare failed; the
  change was refused`, and its `err` names the gateways that did not answer
  (`missing gateway acknowledgements: <nodes>`).
- **`phase="apply"`**: changes committed, but a gateway did not acknowledge
  the apply. It keeps serving the old rules until its cache entries reload
  (at most the 30 s rule cache TTL); its fence expires after 10 s. apid logs `edge rule persisted but fleet
  convergence is incomplete` with the same `err`.

1. Find the gateways named in those log lines and check their listener and
   load (`FaasEdgeRuleListenerStale`, CPU, Postgres connection count).
2. If every gateway is named, check apid's Postgres connection and
   `pg_stat_activity` for a blocked `LISTEN` session.

**Mitigation**: restart the named gateway's gatewayd-internal. If it cannot
recover soon, mark its compute node inactive so apid stops waiting for it.

## FaasEdgeRuleConvergenceStuck

A fence lasts at most 10 s, so a gauge above zero for 10 minutes means
fences keep arriving: changes are being prepared again and again without
an apply, or one account is changing rules continuously.

1. Are changes failing to apply?

   ```promql
   sum by (phase) (increase(apid_edge_rule_convergence_failures_total[10m]))
   ```

   Then find who is changing rules: apid's audit log for `edge_rule.created`,
   `edge_rule.updated` and `edge_rule.deleted` over the last 10 minutes,
   grouped by account.
2. If changes fail in `apply`, follow `FaasEdgeRuleConvergenceFailing`. If
   one client is editing in a loop (an automation retrying), contact the
   account. Its hosts keep serving, but each edit takes a few seconds of
   fleet barrier and holds the account's other edits behind it.

**Mitigation**: stop the loop. Fences clear by themselves 10 s after the last
prepare.
