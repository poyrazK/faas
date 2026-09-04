# FaasOperatorActionTraceCompletenessLow

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`
(recording rule `obs:operator_action_trace_completeness_ratio`
+ alerts `FaasOperatorActionTraceCompletenessLowPage`,
`FaasOperatorActionTraceCompletenessLowWarn`,
`FaasOperatorActionTraceCompletenessLoopStalled` and
`FaasOperatorActionTraceCompletenessFirstTickStalled`).
Metrics: `schedd_operator_action_trace_completeness_ratio{kind}` (gauge,
set by `pkg/sched/operator_intent_completeness.go::observeOperatorIntentCompleteness`
on a 60s tick); sibling per-daemon series
(`apid_operator_action_trace_completeness_ratio`,
`meterd_…`, `gatewayd-internal_…`) are registered but never
`Set` — only schedd runs the driver. Gauge constructor at
`pkg/wire/metrics.go:3211`, pre-instantiation grid at 3232-3233
for the 5 verb-oriented kinds (force_park / force_cold_boot /
force_restart / park_instance / restart_instance).
ADR: PR #1111 (Obs-Meta + Trace-IDs Mega-PR, shipped at 5ead9b7cd),
contract clauses C1-C4 (every `operator.action.<verb>` audit row
carries a non-NULL `events.trace_id`).
PR: #1111 + follow-on `feat/obs-dashboards-alerts` (this file's
source).
Severity: page on Page and FirstTickStalled alerts; warn on Warn alert;
info on LoopStalled.

## Symptom

The trace-completeness signal is `min` over all kinds of
`schedd_operator_action_trace_completeness_ratio` — i.e. the
worst-kind ratio across the 5 verb-oriented kinds
(`force_park`, `force_cold_boot`, `force_restart`, `park_instance`,
`restart_instance`; the 5 `.outcome` variants are pre-instantiated
but only populated when schedd's terminal audit emit runs).
The gauge is pre-instantiated for the 5 verb-oriented kinds via
`pkg/wire/metrics.go::operatorIntentKindClosedSet` (line 3196, the
5-entry closed set feeding the pre-instantiation loop at 3232+) —
every kind reads the Prometheus GaugeVec default (`0.0`, NOT `1.0`)
until schedd's driver ticks and Sets it.
The recording rule `obs:operator_action_trace_completeness_ratio`
(used by both Page and Warn alerts + Dashboard B panel 4) wraps
that `min(...)` in `clamp_min(0.001)` to avoid divide-by-zero in
any downstream ratio comparison, and applies a cold-start guard
based on `schedd_operator_action_trace_completeness_first_tick_completed_total`.
Until the first successful query, the rule returns 1.0 (vacuous truth)
instead of 0.001, so the Page alert does NOT fire false-positive at
t=10m on every restart. Once the first query succeeds, a real all-zero
result is visible. `FaasOperatorActionTraceCompletenessFirstTickStalled`
pages when that first success never arrives, including a pre-Set panic.

Regression test: `pkg/promqlrules/testdata/obs_trace_completeness.test.yml`
(Go-level driver at `pkg/promqlrules/rules_test.go:74` walks the
testdata dir under `-tags=integration`; CI installs promtool
explicitly per `.github/workflows/ci.yml:248-260`).

- **Page**: `obs:operator_action_trace_completeness_ratio < 0.50`
  sustained 10m. At least one verb-oriented kind has fewer than half
  of its 5m-window audit rows carrying a non-NULL `events.trace_id`.
  Cross-daemon correlation is broken — the trace_id is the key that
  joins `audit_log.trace_id` to `events.trace_id`, so a missing
  trace_id means the customer-impacting force-action is uncorrelatable
  to the upstream request that caused it.
- **Warn**: `< 0.95` sustained 30m. Below the contractual floor but
  above the page threshold — the schedd driver, the audit emit site,
  or `pkg/audit.auditKindMetricLabel`'s aliasing of instance-oriented
  kinds onto verb-oriented labels may be drifting. Drill down per
  the Verify section; the next escalation is the page alert.
- **Info** (`LoopStalled`): `time() - schedd_operator_action_trace_completeness_last_success_timestamp_seconds > 180s` for
  5m after at least one successful tick. The producer timestamp is
  written only after a successful query, so scrape activity cannot mask
  a frozen gauge. Routes to the `faas-silent` receiver.
- **Page** (`FirstTickStalled`): the first-success counter remains zero
  for 10m. The platform has no trustworthy completeness signal yet;
  check startup and database connectivity immediately.

The companion dashboard `faas obs trace completeness (PR #1111)`
(`UID faas-obs-trace-completeness-pr-1111`) surfaces panel 1 (the
per-kind ratio timeseries with green/yellow/red overlays), panel 2
(the matching audit-write-rate denominator — kinds with no traffic
read flat-zero here), panel 3 (driver loop freshness in seconds), and
panel 4 (worst-kind stat backed by the recording rule).

## Verify

### Worst-kind localisation (Prometheus)

```bash
# Per-kind completeness (worst first)
curl -fsS --data-urlencode 'query=topk(10, schedd_operator_action_trace_completeness_ratio)' \
  'http://127.0.0.1:9095/api/v1/query'

# Per-kind completeness over time (for the failing kind)
curl -fsS --data-urlencode \
  'query=schedd_operator_action_trace_completeness_ratio{kind="force_park"}' \
  'http://127.0.0.1:9095/api/v1/query_range' \
  -G --data-urlencode 'start=' --data-urlencode 'end=now' --data-urlencode 'step=15s'

# Companion: per-kind audit-write rate (denominator)
curl -fsS --data-urlencode \
  'query=topk(8, sum by (kind) (rate({__name__=~".*_audit_log_write_total"}[5m])))' \
  'http://127.0.0.1:9095/api/v1/query'

# Recording rule value (what the Page + Warn alerts evaluate)
curl -fsS --data-urlencode 'query=obs:operator_action_trace_completeness_ratio' \
  'http://127.0.0.1:9095/api/v1/query'
```

A kind missing from the second query while present in the third is
a real "no traffic in window" reading — not a completeness violation.
A kind whose ratio sits at 1.000 even when its write rate is >0
indicates a vacuous-truth default; switch the gauge to its
`WithLabelValues(...)` default `0` (pre-instantiation) before
debugging.

### SQL-side gap (Postgres)

```bash
sudo -u postgres psql -d faas -c "
  SELECT kind,
         count(*) FILTER (WHERE trace_id IS NULL) AS trace_id_null,
         count(*) AS total,
         round(100.0 * count(*) FILTER (WHERE trace_id IS NULL) / count(*), 2)
           AS null_pct
  FROM events
  WHERE kind LIKE 'operator.action.%'
    AND at > now() - interval '5 minutes'
  GROUP BY kind
  ORDER BY null_pct DESC
  LIMIT 20;
"
```

A row with `null_pct` ≥ 50% is the source of the page. A row with
`null_pct` ≥ 5% but < 50% is the source of the warn.

### Migration sanity (events.trace_id regex)

The column accepts a 32-char W3C OTel hex. Verify the migration
shape is still as expected:

```bash
sudo -u postgres psql -d faas -c "\d events" | grep trace_id
sudo -u postgres psql -d faas -c "
  SELECT conname, pg_get_constraintdef(oid)
  FROM pg_constraint
  WHERE conrelid = 'events'::regclass AND contype = 'c'
    AND pg_get_constraintdef(oid) ILIKE '%trace_id%';
"
```

The migration `00486_events_operator_intents_trace_id.sql`
(Mermaid docstring at the file head lists every prior renumber)
is the source of the regex. If you see other shapes, file an ADR
before widening (CLAUDE.md §17 G7).

## Check

### Driver-loop freshness (info-tier alert)

```bash
# Producer freshness (written after a successful query)
curl -fsS --data-urlencode \
  'query=time() - schedd_operator_action_trace_completeness_last_success_timestamp_seconds' \
  'http://127.0.0.1:9095/api/v1/query'

# First-tick status (0 means no successful observation yet)
curl -fsS --data-urlencode \
  'query=schedd_operator_action_trace_completeness_first_tick_completed_total' \
  'http://127.0.0.1:9095/api/v1/query'

journalctl -u schedd --since '-15m' --no-pager \
  | grep -E 'observability|trace_completeness|operator_intent_completeness'
```

A freshness value above 180s with empty slog output means the
goroutine has crashed or been starved by the connection pool.

### Per-emit-site evidence

The schedd outcome emit is at
`pkg/sched/operator_intent_subscriber.go:238-240` — the lift
`if intent.TraceID != nil { data["trace_id"] = *intent.TraceID }`.
If the worst kind is verb-oriented (`force_*`), this is the
suspect. The apid request-side emit goes through
`pkg/audit/auditKindMetricLabel` (audit.go:351-390) — instance-
oriented kinds (`park_instance`, `restart_instance`) are aliased
onto the verb-oriented metric labels there. If the worst kind is
instance-oriented, this is the suspect.

### Cross-daemon noise

```bash
curl -fsS --data-urlencode \
  'query={__name__=~".*_operator_action_trace_completeness_ratio"}' \
  'http://127.0.0.1:9095/api/v1/query'
```

Filter for non-`schedd_` series: those are
`WithLabelValues(...)` defaults on gauges that no daemon ever sets.
They are noise — only `schedd_*` is real.

## Silence

```bash
amtool silence add \
  --matchers='alertname=~"FaasOperatorActionTraceCompleteness.*"' \
  --duration=30m \
  --comment='investigating schedd trace_completeness drop'
```

The regex matcher pairs all four (`Page`, `Warn`, `LoopStalled`,
`FirstTickStalled`)
in one silence — page-warn auto-inhibit (alertmanager
`family=obs_trace, component=schedd`) means the warn is suppressed
while the page is firing anyway, so silencing all four is safe.

## Recover

### Page (completeness < 50%)

1. **Bounce schedd** (the gauge is pre-instantiated, so no warm-up
   gap): `systemctl restart schedd`. The driver's first pass after
   restart is immediate.
2. **Confirm** the recording rule value rebounded:
   `curl -fsS --data-urlencode 'query=obs:operator_action_trace_completeness_ratio' http://127.0.0.1:9095/api/v1/query`.
3. If the bounce did not help, the audit emit site is dropping
   `trace_id` — that is a code change; file an `obs-meta` issue.

### Warn (completeness < 95%)

Monitor; no immediate action is mandated. If the next escalation
trips the page, follow Page recovery.

### Info (loop stalled > 3 ticks)

1. **Bounce schedd** if `journalctl -u schedd` shows a crash log.
2. **If pool exhaustion**: drain and let the connection-pool
   supervisor rebuild (the gauge pre-instantiation makes this safe).
3. **If neither**: file an `obs-meta` issue with the slog capture;
   the driver is silently wedged.

### Page (first tick stalled)

1. Check `journalctl -u schedd` for a startup panic or a blocked
   `operator_intent_completeness` query.
2. Verify Postgres is reachable and the schedd pool is not exhausted.
3. Restart schedd after correcting the dependency; confirm
   `schedd_operator_action_trace_completeness_first_tick_completed_total`
   becomes `1` and the last-success timestamp advances.

## Follow-up

File any audit-emit-site fix as a follow-up issue tagged `obs-meta`.
ADR-091 D20.4 (plus its Amendment 2) holds the closed-set contract
for `auditKindMetricLabel`. PR #1111 §C1-C4 is the contract for
trace_id propagation; deviations from it require an ADR.
