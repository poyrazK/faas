# FaasOperatorActionTraceCompletenessLow

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`
(recording rule `obs:operator_action_trace_completeness_ratio`
+ alerts `FaasOperatorActionTraceCompletenessLowPage`,
`FaasOperatorActionTraceCompletenessLowWarn`,
`FaasOperatorActionTraceCompletenessLoopStalled`).
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
Severity: page on Page alert; warn on Warn alert; info on LoopStalled.

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
any downstream ratio comparison, and applies a `unless on() (
sum == 0) or on() vector(1)` cold-start guard: when EVERY kind
is still at the Prometheus GaugeVec default (0 — i.e., the schedd
driver has not yet ticked after a fresh boot), the rule returns
1.0 (vacuous truth) instead of 0.001, so the Page alert does NOT
fire false-positive at t=10m on every restart. A panic mid-tick
that leaves some kinds Set and others at the Prometheus default
still produces `sum != 0`, so the guard does NOT engage and the
alert fires as before — preserving the panic-resilience contract
pinned at `pkg/sched/operator_intent_completeness_test.go:118`.

**Known limitation (finding #1 of `/code-review 1162`):** the guard
also swallows the *pre-Set* panic case — when schedd's driver
panics anywhere before reaching the Set loop at
`pkg/sched/operator_intent_completeness.go:235-238`, every kind
stays at the Prometheus default 0, `sum == 0` evaluates to true,
the guard engages, and the Page alert stays silent. The
compensating signal is `FaasOperatorActionTraceCompletenessLoopStalled`
(info-tier, fires at `time() - timestamp(gauge) > 180 for 5m`) —
but only when the schedd daemon has fully stopped scraping. For
the "driver panicked, daemon still up" case there is currently
no automatic alert; operators rely on `journalctl -u schedd` slog
capture (the panic logs at the goroutine crash site). Tightening
this would require a separate "first driver tick completed"
counter — out of scope for this fix.

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
- **Info** (`LoopStalled`): `time() - timestamp(gauge) > 180s` for
  5m. The driver goroutine has stopped updating the gauge for >3
  ticks. The page+warn alerts may be silent even though the system
  is broken — Prometheus scrapes see the last-written value frozen.
  Routes to the `faas-silent` receiver.

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
# Stalest kind (max() picks the worst, not an arbitrary one)
curl -fsS --data-urlencode \
  'query=max(time() - timestamp(schedd_operator_action_trace_completeness_ratio))' \
  'http://127.0.0.1:9095/api/v1/query'

# Per-kind freshness if a single kind is suspect
curl -fsS --data-urlencode \
  'query=time() - timestamp(schedd_operator_action_trace_completeness_ratio)' \
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

The regex matcher pairs all three (`Page`, `Warn`, `LoopStalled`)
in one silence — page-warn auto-inhibit (alertmanager
`family=obs_trace, component=schedd`) means the warn is suppressed
while the page is firing anyway, so silencing all three is safe.

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

## Follow-up

File any audit-emit-site fix as a follow-up issue tagged `obs-meta`.
ADR-091 D20.4 (plus its Amendment 2) holds the closed-set contract
for `auditKindMetricLabel`. PR #1111 §C1-C4 is the contract for
trace_id propagation; deviations from it require an ADR.
