# RequestBudgetExhausted

Source: `gateway_request_budget_seconds{outcome="exceeded"}` and
`apid_request_budget_seconds{outcome="exceeded"}` histogram series
+ `gateway_request_budget_exceeded_total{hop}` /
`apid_request_budget_exceeded_total{hop}` counter families.
Dashboard panel: `deploy/grafana/request-budget.json` (ADR-093).
Spec: §4.1.2.16 + ADR-093. Metric names match §12.
Severity: **ticket** (per-app; sustained spikes are **page**).

## What this is

ADR-093 ships an end-to-end request budget for every customer request
that flows through `gatewayd-public` (default 3 s) and every apid
admin request (default 5 s). The budget is a hard wall-clock
deadline installed at the edge; downstream hops (DB, gRPC, HTTP)
tighten themselves against it via `reqbudget.WithCeiling` /
`WithOverhead`. When the budget fires, the customer sees a 504 RFC
7807 envelope with `code: request_budget_exceeded` BEFORE the
customer handler runs — the goal is bounded resource pin per
request, not customer-visible timer logic.

The budget is sourced from:

  1. `edge_rules.kind = budget` — per-(hostname, path, method) rule
     that pins the budget (e.g. "POST /payment → 3 s"). This is
     the documented customer-facing surface.
  2. `Plan.RequestBudgetMs` — per-plan default fallback. Zero =
     fall back to `RequestBudgetDefault` (3 s).
  3. `RequestBudgetDefault` (3 s) — the last-resort fallback when
     neither rule nor per-plan override is set.

Per-app overrides win over per-plan defaults; per-plan defaults
win over the platform default.

**Where the budget is stamped matters** (ADR-093 amendment,
2026-09-03). `gatewayd-public` stamps only `RequestBudgetMax` (30 s)
as a liveness backstop — it cannot resolve the app, so it cannot see
a `kind=budget` rule. The authoritative budget is stamped one hop
later by `gatewayd-internal`'s `applyEdgeRuleBudget`, which is where
`budget_stamped` is logged and where the rule actually applies.

Before that amendment `gatewayd-public` stamped 3 s, and because
`reqbudget` derives a *child* budget from the parent's remaining
time, a `kind=budget` rule could only ever tighten the budget, never
widen it. If you are debugging a deployment that predates the
amendment, a rule that appears applied (`source:rule` in
`budget_stamped`) but has no effect on observed latency is that bug,
not a rule problem.

## Symptom

The customer reports 504 responses with `code: request_budget_exceeded`.
The dashboard panel shows a non-zero rate on
`gateway_request_budget_exceeded_total{route="forward"}` (gatewayd-public)
or `apid_request_budget_exceeded_total{route="admin"}` (apid).

A spike indicates either:

  - A specific downstream hop is the bottleneck — look at the
    `hop=` label on the counter. `hop=gateway` = middleware wrote
    the 504 without a downstream call firing (the customer's own
    handler starved the budget). `hop=db` = a SQL call exceeded.
    `hop=stream` = the response body took too long. `hop=http` =
    an outbound HTTP call from the customer handler exceeded.
  - The budget is too tight for the workload — e.g. a 1 s
    budget on `POST /export.csv` that's known to take 5 s.

## Verify

```bash
# Per-hop breakdown — what's exhausting the budget?
curl -fsS http://127.0.0.1:9090/metrics | grep '^gateway_request_budget_exceeded_total' | grep -v ' 0$'

# Per-route p50/p95 — where is the tail?
curl -fsS http://127.0.0.1:9090/metrics | grep '^gateway_request_budget_seconds_bucket'

# Operator log — the budget_exceeded log line carries
# route, endpoint, budget_ms, hop.
journalctl -u faas-gatewayd-public --since '-1h' --no-pager \
  | grep -E 'budget_exceeded .* code=request_budget_exceeded'

# Per-plan override
psql -At -c "select name, request_budget_ms from plans order by name;"

# Per-route rule
psql -At -c "select id, kind, match_hostname, match_path, match_method, action from edge_rules where kind = 'budget';"
```

## Mitigation

### If the budget is too tight

Two options:

  1. **Per-app rule (preferred)**: add an `edge_rule` of
     `kind=budget` with `action.budget.budget_ms` set to the desired
     wall-clock ceiling. The rule must beat the per-plan default —
     match by `(hostname, path, method)` to scope narrowly.

     ```
     gregale edge-rules create --app <slug> --kind budget \
       --match-host <app>.gregale.dev --match-path /slow-route \
       --budget-ms 15000
     ```

     Add `--budget-allow-override-header` to let callers tune the
     budget per request; it defaults to `x-faas-budget-ms`. Note that
     the override header is inert until a `kind=budget` rule matches
     the request — there is no rule-free way to widen the budget.
  2. **Per-plan default**: bump `plans.request_budget_ms` for the
     affected plan. Only do this when the spike is fleet-wide, not
     per-app.

The platform ceiling is `RequestBudgetMax = 30 s` (defined in
`pkg/api/limits.go`); budgets above this are clamped at apply time.

### If a specific downstream hop is the bottleneck

Use the `hop=` label to identify the slow leg:

  - `hop=db` — Postgres is slow. Check pg_stat_activity, slow query
    log, pool saturation. The DB call seam is `pkg/db.WithBudget`
    (PR-E). The overhead reservation is 10 ms per call; the actual
    round-trip is whatever Postgres takes.
  - `hop=http` — outbound HTTP from the customer's handler is
    slow. The customer owns this; the runbook surfaces it.
  - `hop=stream` — the response body took longer than the budget
    to drain. The streaming writer's per-flush deadline was
    clamped to the budget's remaining time (PR-C); the customer
    is sending a slow stream.
  - `hop=gateway` — the customer's own handler exhausted the
    budget without a downstream call firing. This is by design
    (the customer must respect the budget); the runbook doesn't
    have a server-side fix.

### If a budget rule is wrong

```sql
-- Disable a budget rule (preserves audit trail).
update edge_rules
   set enabled = false,
       updated_at = now()
 where id = '<rule-id>';

-- Tighten or relax the budget ceiling.
update edge_rules
   set action = jsonb_set(action, '{budget,budget_ms}', '5000'),
       updated_at = now()
 where id = '<rule-id>';
```

The gateway reloads the rule on the next `compileBudgetRules`
cycle (issue #678 §6 — pg_notify `edge_rule_changed` invalidates
the host's cache entry; the next request picks up the new rule).

### If the budget middleware itself is broken

`gateway_request_budget_exceeded_total` should never go above the
gateway's request volume — every budget fire increments it. If
it's stuck at zero while customers are seeing 504s, the middleware
isn't firing. Check:

```bash
# Was the middleware wired? The counter series must exist.
curl -fsS http://127.0.0.1:9090/metrics | grep '^gateway_request_budget_'

# Any 504 in the access log with the wrong code?
journalctl -u faas-gatewayd-public --since '-1h' --no-pager \
  | grep ' 504 ' | grep -v 'request_budget_exceeded'
```

If the counter series is missing, the BudgetMiddleware wiring
in `cmd/gatewayd-public/main.go` regressed — check the PR-B
git log for the merge that dropped the wrap.

## Postmortem follow-up

  1. If a hop drove the spike, open a backlog issue on the
     downstream team. The budget is a quality primitive, not a
     substitute for a fix.
  2. If a customer complained, document the budget rule change in
     `docs/edge-rule-changelog.md` (additive, append-only).
  3. If the spike was fleet-wide, add a §17 lean for "consider
     raising Plan.RequestBudgetMs" — the §4.1.2.16 SLA contract
     should match customer expectations.

## Cross-references

  - ADR-093 (this runbook's source of truth).
  - §4.1.2.16 — edge-rule kind=budget spec.
  - §12 — metric name contract.
  - `pkg/gateway/handler_apply_edge_rule_budget.go` — the hot-path
    applier.
  - `pkg/reqbudget/` — the library.
  - `pkg/api/limits.go::RequestBudgetDefault` /
    `RequestBudgetMax` / `RequestBudgetApidDefault` — the limits
    table entries.
