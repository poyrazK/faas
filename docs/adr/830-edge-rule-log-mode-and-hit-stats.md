# ADR-830: Edge-rule log mode and per-rule hit stats

- **Status:** accepted
- **Date:** 2026-10-09
- **Related:** ADR-091 (edge rules), ADR-128 (validate observe/warn modes),
  ADR-831 (versions), ADR-832 (match expressions)

## Context

The only way to try a rule on live traffic is `kind=validate`'s observe
mode. Every other rule takes effect the moment it is saved, so an operator
adding a geo block, a JWT gate or a maintenance window cannot see what it
would catch before it catches it. The trace dry run (ADR-831 work) answers
"what happens to this one request", not "how much real traffic would this
rule hit". Operators also cannot tell which rules ever match: metrics are
per kind, so dead or overly broad rules are invisible.

## Decision

1. Every rule gains **`mode`**: `enforce` (default, today's behaviour) or
   `log`. A `log` rule is matched exactly like an enforced rule (selectors,
   ADR-832 condition, owner scoping) but never acts and never shadows other
   rules: the gateway picks the effective rule of each kind from enforced
   rules only, and separately records a match for the first log-mode rule
   of that kind that matches. `validate_mode` keeps its own meaning for
   `kind=validate`.

2. **Per-rule hit counts.** Each gateway counts, per rule, requests the rule
   matched (`matched` for enforced rules, `logged` for log-mode rules). A rule
   is counted at most once per request even when a kind is looked up more
   than once. Counts accumulate in memory, keyed by rule ID (bounded by the
   loaded rule set), and a background flusher adds them to Postgres once a
   minute in one batch, off the request path. Hourly buckets in
   `edge_rule_hit_counts(rule_id, app_id, bucket_start, outcome)` are kept
   for 14 days. A failed flush keeps the counts for the next attempt, up to
   a bounded number of rules; beyond that the oldest are dropped (counts are
   telemetry, not billing).

3. Prometheus does not get a `rule_id` label: rules are unbounded across
   tenants, so per-rule data lives in Postgres. Per-kind metrics are
   unchanged.

4. Counts are read with `GET /v1/apps/{slug}/edge-rules/stats?window=1h|24h|7d`
   and `gregale edge-rules stats`; the trace simulator reports log-mode rules
   as logged, not enforced. `mode` is part of the ADR-831 rule-set snapshot.

## Consequences

- Rules can be shadow-tested on real traffic, then switched to `enforce` (a
  normal update, versioned and dry-runnable).
- Operators can find dead rules (no matches) and over-broad ones.
- A matching request costs one atomic increment per counted rule; Postgres
  sees one batched write per gateway per minute.
- Counts are approximate across a gateway crash (up to one minute lost).
