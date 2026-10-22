# ADR-1192: Alerts on edge-rule activity

- **Status:** accepted
- **Date:** 2026-10-11
- **Related:** ADR-960 (log mode and hit counts), ADR-961 (versioned rule
  sets), ADR-964 (security events), ADR-045 (alert rules)

## Context

Edge rules now count every match (ADR-960) and sample matched requests
(ADR-964), but someone has to open the dashboard to notice that a block
rule suddenly matches thousands of requests, that a log-mode rule would
block real traffic before it is enforced, or that a rule stopped matching
after a change. The alert pipeline already evaluates per-app metrics and
delivers signed webhooks.

## Decision

1. **Two alert metrics.** `edge_rule_hits` counts enforced matches;
   `edge_rule_log_hits` counts log-mode matches, i.e. what a log-mode rule
   would have done. Both read the ADR-960 hourly hit buckets — no new
   counters on the request path.

2. **Optional rule scope.** `edge_rule_id` limits the count to one rule of
   the app; without it every rule of the app is summed. The id is checked
   to belong to the app when the alert is created and is immutable
   afterwards. It is deliberately not a foreign key: an ADR-961 rule-set
   restore deletes and re-inserts rules under the same ids, and a cascade
   would silently delete the alert. A rule that no longer exists counts
   zero.

3. **Windows match the data.** Hit counts are hourly buckets kept for 14
   days, so these metrics take `window_spec` 1h, 6h, 24h or 7d. A window is
   rounded out to whole clock hours (a 1h window read at 10:20 covers
   09:00–10:20). Hits reach Postgres within a minute of the match.

4. **Webhook only, app scoped.** Hit counts are driven by outside traffic,
   so like pre-auth and workflow metrics they can never roll back, demote
   or promote a deployment. The metric family cannot change on update.
   `alert_rules_edge_rule_chk` enforces the shape in the database.

5. **Payload.** The webhook body carries `edge_rule_id` when set, next to
   the observed count.

## Consequences

- "Tell me when my block rule matches more than 1,000 requests an hour"
  and "tell me if this log-mode rule would block anything today" are one
  alert each, reachable from the API, CLI (`gregale alerts add --metric
  edge_rule_hits --edge-rule-id …`) and SDKs.
- A throttle rule's hits are the requests it applies to, not only those
  it rejected; a rejection-only signal would need its own counter.
- Sub-hour windows are not available until hit counts gain finer buckets.

## Amendment (2026-10-14): `edge_rule_activity`

A third metric, `edge_rule_activity`, counts a rule's matches in either
mode (enforced + log-mode). A rule is in one mode at a time, so for a
scoped alert it is simply "how often this rule matched", and it keeps
working when the rule moves from log mode to enforce — which the two
mode-specific metrics do not. Same shape rules as the others: app-scoped,
webhook-only, 1h/6h/24h/7d windows (`alert_rules_edge_rule_chk`).
