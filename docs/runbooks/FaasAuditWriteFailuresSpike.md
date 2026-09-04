# FaasAuditWriteFailuresSpike

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`
(alert `FaasAuditWriteFailuresSpike`).
Metrics: `<daemon>_audit_log_write_failures_total{endpoint, kind,
error_class}` (counter, declarations at `pkg/wire/metrics.go:1515`,
pre-instantiation grid at lines 3215-3222 spanning the
`auditEndpointClosedSet` × `auditKindClosedSet` ×
`auditErrorClassClosedSet` Cartesian — declarations at lines
3158 / 3159 / 3178). The metric is **per-daemon prefix** — actual
series names are `apid_audit_log_write_failures_total`,
`schedd_audit_log_write_failures_total`, etc. The cross-daemon
regex `{__name__=~".*_audit_log_write_failures_total"}` is the
alert's surface.
ADR: ADR-035 (best-effort audit), PR #1111 §C2-C4 (metric
contract).
Severity: warn.

## Symptom

Combined audit-write failure rate across every daemon emitting
audit rows (apid, schedd, meterd, gatewayd-internal — see
`pkg/wire/metrics.go::auditEndpointClosedSet`) has exceeded
5 failures/min over the rolling 5m window for 5m. The threshold
matches `FaasApidAuditWriteFailures` for consistency.

This alert is **deliberately not paired** with
`FaasApidAuditWriteFailures` (which covers apid alone). Both fire
under `family=audit_write` but with different `component` labels
(`apid` vs `platform`). The load-bearing reason they do NOT
suppress each other is that BOTH are `severity=warn` and the
alertmanager inhibit rule (`deploy/ansible/roles/alertmanager/
templates/alertmanager.yml.j2:124-136`) requires
`source.severity=page` to engage — no page-tier `audit_write`
alert exists, so the cross-daemon warn stays independently
visible regardless of the `component` label values. Component
labels here route the email (platform-wide vs apid-only queue);
severity is the inhibit lever. The point of this warn is
precisely to surface non-apid spikes — schedd / meterd /
gatewayd-internal audit-write failures that the apid-only alert
misses.

The audit emit is best-effort by design (ADR-035): the
user-facing action has already returned 200 to the customer by
the time `AppendEvent` runs, so a failed insert logs WARN and
bumps the counter without rolling back the action. A sustained
non-zero rate therefore means audit rows are silently missing
while customer traffic looks healthy — ticket-tier operational
signal, not page-tier outage.

The companion dashboard `faas audit-write fidelity (PR #1111)`
(`UID faas-audit-write-fidelity-pr-1111`) surfaces panel 1 (writes
by `endpoint`), panel 2 (failures by `error_class`), panel 3
(failure ratio with green<0.001 / yellow<0.01 / red≥0.01
threshold bands), and panel 4 (top-8 failing `kind`).

## Verify

### Localise by endpoint + error_class

```bash
curl -fsS --data-urlencode \
  'query=topk(10, sum by (endpoint, error_class) (rate({__name__=~".*_audit_log_write_failures_total"}[5m])))' \
  'http://127.0.0.1:9095/api/v1/query'
```

The result tells you which daemon (`endpoint`) and which error
class (`sqlstate_23514` / `sqlstate_23505` / `timeout` / `other`)
is driving the spike.

### Localise by kind

```bash
curl -fsS --data-urlencode \
  'query=topk(10, sum by (kind) (rate({__name__=~".*_audit_log_write_failures_total"}[5m])))' \
  'http://127.0.0.1:9095/api/v1/query'
```

A spike concentrated on `force_park` / `force_cold_boot` /
`force_restart` (and their `.outcome` siblings) points at
operator-action audit emits — `cmd/schedd/` or
`pkg/sched/operator_intent_subscriber.go:238-240` (the lift to
`events.trace_id`). A spike on `auth.*` or `account.*` points
at apid's HTTP handlers.

### SQL-side correlation (Postgres)

```bash
sudo -u postgres psql -d faas -c "
  SELECT kind, sqlstate,
         count(*) AS failures,
         max(at) AS most_recent
  FROM events
  WHERE at > now() - interval '15 minutes'
  GROUP BY kind, sqlstate
  HAVING count(*) > 0
  ORDER BY count(*) DESC
  LIMIT 20;
"
```

Map `sqlstate`:

- `23514` → `check_violation`. The `events.trace_id` regex from
  migration `00486_events_operator_intents_trace_id.sql`
  rejecting an inserted value. The trace_id being inserted is
  not a 32-char W3C OTel hex.
- `23505` → `unique_violation`. The audit emit path's known
  single-INSERT retry race (see the precedent comment at
  `faas.rules.yml:358-369`). Usually transient.
- `57014` / timeout-class → connection pool exhaustion. Cross-
  check `FaasApiAvailabilityLow`.
- `(no sqlstate)` or other → unclassified; daemon slog holds
  the actual error.

### Daemon-side evidence

```bash
# Sweep every daemon that emits audit rows.
for unit in apid schedd meterd gatewayd-internal; do
  echo "--- ${unit} ---"
  journalctl -u ${unit} --since '-15m' --no-pager \
    | grep -iE 'audit: append event|audit_log_write_failures'
done
```

A failed `AppendEvent` produces a structured WARN log with
`kind` / `endpoint` / `error_class` / `err` keys. If the same
`error_class` is failing across many `endpoint`s, the cause is
global (Postgres, WAL, pool).

## Check

### Postgres health

```bash
sudo -u postgres psql -d faas -c "
  SELECT count(*) AS recent_events
  FROM events
  WHERE at > now() - interval '5 minutes';
"

sudo -u postgres psql -c "
  SELECT count(*), state
  FROM pg_stat_activity
  WHERE datname='faas'
  GROUP BY state;
"
```

A healthy control plane writes many events rows per second
(every auth.* / cron.* / edge_rule.* emission). A drop to ~0 with
sustained `*_audit_log_write_failures_total` is the canonical
sign that Postgres is rejecting inserts across the board.

### Migration 00486 sanity (events.trace_id regex)

```bash
sudo -u postgres psql -d faas -c "\d events" | grep trace_id
sudo -u postgres psql -d faas -c "
  SELECT conname, pg_get_constraintdef(oid)
  FROM pg_constraint
  WHERE conrelid = 'events'::regclass AND contype = 'c'
    AND pg_get_constraintdef(oid) ILIKE '%trace_id%';
"
```

If you see a regex shape other than 32-char OTel hex, file an
ADR (CLAUDE.md §17 G7) before widening — the migration header
documents the upstream renumber chain (00456 → 00469 → 00472 →
00475 → 00484 → 00486).

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasAuditWriteFailuresSpike' \
  --duration=30m \
  --comment='investigating cross-daemon audit persistence failures'
```

If the underlying cause is `sqlstate_23514` and you've ruled out
a transient regex drift, silence is the right move until a code
fix lands — the audit emit path's `AppendEvent` does not backfill
on recovery (ADR-035 best-effort policy).

## Recover

The alert clears automatically once the rolling 5m cross-daemon
rate falls below 5/min and the `for: 5m` window expires. Recovery
actions, in order, by `error_class`:

1. **sqlstate_23514 (check_violation)**: the offending emit site
   is generating a malformed trace_id. The expected shape is a
   32-char lowercase W3C OTel hex
   (`^[0-9a-f]{32}$` — pinned by `00486_*_test.go`'s pgtest).
   Trace by `kind`: the offending audit emit path is the one
   shipping a non-conforming trace_id. Likely a 32-char
   truncated UUID or a 36-char hyphenated UUID; both fail the
   regex. Code-fix: emit-site-only, not a regex widening
   (regex widening is a security-sensitive decision — file an
   ADR first).
2. **sqlstate_23505 (unique_violation)**: usually transient — a
   retry race against the audit emit's single-INSERT path.
   Ride it out; alert clears within one or two 5m windows.
3. **timeout**: cross-check `FaasApiAvailabilityLow`. The
   standard Postgres recovery runbook (`docs/ops/`) covers
   WAL replication lag, connection-pool exhaustion under burst
   load, and `events` table bloat.
4. **other**: unclassified. File an issue with the daemon slog
   capture — `endpoint` + `kind` + `error_class` + `err` keys
   are the minimum payload to triage downstream.

## Follow-up

ADR-035 best-effort policy applies: audit rows missing for the
alert window are **not** retroactively backfilled. The
customer-facing audit log (`GET /v1/audit-events`) simply has a
gap. Do not promise the customer a backfill — it is not part of
the contract.

If `sqlstate_23514` is structural (the regex is too narrow for
some legitimate caller), the fix is an ADR (CLAUDE.md §17 G7) for
widening, plus a new migration that drops the existing CHECK and
re-creates it with the broader regex. PR #1111 C2-C4 hold the
contract for `trace_id` propagation — deviations from the
32-char OTel shape are a contract change.
