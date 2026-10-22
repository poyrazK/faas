# FaasEdgeRuleCompileError

Source: dashboard `faas-edge-rules-pr-b` panel 4
(`deploy/grafana/edge-rules.json`) — the stat panel's `gt 0` color
override paints any non-zero value red on the first increment, so
this is a fast signal. Also surfaces as Prometheus alert rules in
`deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `gateway_edge_rule_compile_error_total{kind}`.
Spec: §12 + ADR-091 Amendment 1 (`pkg/gateway/metrics.go::ObserveEdgeRuleCompileError`
doc-comment: "a rule shipped broken is an actionable correctness
signal, not a headroom signal").
Severity: **page** for access-gating kinds (`jwt`, `ip`, `waf`) via
`FaasEdgeRuleAuthCompileError`: a dropped access rule is not enforced, so
its route serves without the check. Other kinds are warn via
`FaasEdgeRuleCompileError` (ADR-1236).

## Symptom

Dashboard panel 4 (stat) shows any non-zero value with a red background.
The counter increments **once per dropped rule** at `loadHost` time
(`cmd/gatewayd-internal/edge_rules.go::loadHost`), not once per host —
so a single broken rule across N cached hosts is N ticks. The `kind=`
label tells you which family of rules is broken: `route`, `rewrite`,
`redirect`, `headers`, `cors`, `jwt`, `ip`.

A compile error means a rule was shipped to production with a
malformed `match_path` glob (route/rewrite/redirect/headers), a malformed
CIDR (ip), a malformed JWKS URL (jwt), or a malformed
`allow_origins`/`allow_methods` JSON array (cors). The rule is **dropped
at load time** — the customer sees a clean 404 / fall-through response,
not a 5xx. The danger is silent: the rule exists, the row is there, but
no traffic matches it because `path.Match` rejected the pattern.

## Verify

```bash
# Per-kind error count.
curl -fsS http://127.0.0.1:9090/metrics | grep -E '^gateway_edge_rule_compile_error_total' | grep -v ' 0$'

# Operator log — the WARN line carries rule_id + glob + err.
journalctl -u faas-gatewayd-internal --since '-1h' --no-pager \
  | grep -E 'edge rule path glob parse error; rule dropped'

# Cross-check: is the rule still in the database?
PGPASSWORD=$FAAS_PG_PASSWORD psql -h localhost -U faas \
  -c "select id, app_id, kind, match_host, match_path, enabled from edge_rules where id = '<rule_id-from-log>';"
```

## Check

- **kind=route / rewrite / redirect / headers** — `match_path` glob
  failed `path.Match` (unmatched `[` / `]`, unmatched `{` / `}`, trailing
  backslash). Fix the glob in apid; save the rule; the WARN line in
  `journalctl` already has the offending `glob` string.
- **kind=cors** — `allow_origins` or `allow_methods` is not a valid
  JSON array, or `allow_origins` contains `*` together with
  `allow_credentials = true` (the §11 ship-blocker — see ADR-091 D12).
  Either drop `*` or drop `allow_credentials`.
- **kind=jwt** — `jwks_url` does not start with `https://`, or the JWKS
  fetch URL is otherwise malformed. Fix the URL in apid.
- **kind=ip** — CIDR failed `net.ParseCIDR`. Common typo: missing the
  `/N` suffix (e.g. `10.0.0.0` instead of `10.0.0.0/8`). Fix in apid.

After fixing the rule, the WARN line stops appearing on the next
cache miss. The counter, however, is **monotonic** (Prometheus
counter semantics) — it accumulates forever and is not reset by a
rule fix. The alert *fires* on the rate of new ticks, not the absolute
count. The dashboard's stat panel will continue to show the cumulative
count until the process restarts; an operator can note "this is the
historical tally since boot, current rate is zero" in the alert ack.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasEdgeRuleCompileError' \
  --duration=30m \
  --comment='patching broken rule; replacement in flight'
```

## Recover

1. Identify the offending rule from the WARN line (`rule_id` field).
2. Fix the rule in apid (correct the glob, drop the bad CIDR, fix
   the JWKS URL, etc.) — or delete the rule if the customer's intent
   has changed.
3. The cache invalidation is **automatic** — the apid write path emits
   `db.NotifyEdgeRuleChanged` (`cmd/gatewayd-internal/backend.go`),
   which triggers `gatewaydEdgeRules.Reset()` and forces the next
   request to re-load and re-compile. Verify the WARN line stops
   appearing in `journalctl` within 60 s of the apid write.
4. Confirm the counter stops incrementing by sampling `/metrics`
   twice, 1 minute apart, after the fix.