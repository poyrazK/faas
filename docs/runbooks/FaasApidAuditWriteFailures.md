# FaasApidAuditWriteFailures

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `apid_audit_write_failures_total{account_id}` and
`apid_audit_write_failures_duration_seconds{result}` (apid `/metrics`).
Issue: #278.
Severity: warn.

## Symptom

The apid audit event append failure rate has exceeded 5 failures per
minute over the rolling 5-minute window for 5 m. The metric label is
the customer `account_id` (or `"anonymous"` for system-fired events,
`"__other__"` if the bounded admission set is full — see *Check*).

The audit emit is **best-effort by design** (ADR-035): the auth
action has already returned 200 to the customer by the time
`AppendEvent` runs, so a failed insert logs WARN and bumps the
counter without rolling back the action. This alert is therefore a
**ticket-tier operational signal**: the customer is unaffected, but
audit rows are silently missing. A sustained rate means the SOC2
audit trail has gaps.

## Verify

```bash
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=sum%28rate%28apid_audit_write_failures_total%5B5m%5D%29%29'

curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=topk%2810%2C+sum+by+%28account_id%29+%28rate%28apid_audit_write_failures_total%5B5m%5D%29%29%29'

curl -fsS 'http://127.0.0.1:9101/metrics' | grep -E '^apid_audit_write_failures_total'
```

The third call reads apid's loopback metrics endpoint directly. The
two Prometheus queries show (a) the platform-wide rate and (b) the
top-10 offenders, which is the fastest way to localize a single
customer driving the failure stream.

## Check

### Per-customer drill-down (the issue's headline use case)

Replace `ACCOUNT_ID` with the customer's UUID. The label set is
identical in Prometheus and on the wire, so the value can be pasted
verbatim from `/v1/account` (`account.id`):

```bash
ACCOUNT_ID="<paste from /v1/account>"

# Their audit-write failure rate
curl -fsS --data-urlencode "query=rate(apid_audit_write_failures_total{account_id=\"${ACCOUNT_ID}\"}[5m])" \
  'http://127.0.0.1:9095/api/v1/query'

# Their request-failure stream (companion counter, same label;
# code="err" is the invariant — see ADR-039 §Consequences)
curl -fsS --data-urlencode "query=sum by (route) (rate(apid_request_failures_total{account_id=\"${ACCOUNT_ID}\",code=\"err\"}[5m]))" \
  'http://127.0.0.1:9095/api/v1/query'

# Their audit-write latency p95
curl -fsS --data-urlencode "query=histogram_quantile(0.95, sum by (le) (rate(apid_audit_write_failures_duration_seconds_bucket{result=\"failed\"}[5m])))" \
  'http://127.0.0.1:9095/api/v1/query'
```

### Daemon-side evidence

```bash
journalctl -u apid --since '-15m' --no-pager | grep -i 'audit: append event'
```

A failed `AppendEvent` produces a structured WARN log with
`kind` / `subject` / `err` keys. If the same `kind` is failing for
many customers, the cause is global (Postgres, WAL, pool).

### Postgres health

```bash
sudo -u postgres psql -c "SELECT count(*) FROM events WHERE at > now() - interval '5 minutes'"
sudo -u postgres psql -c "SELECT count(*), state FROM pg_stat_activity WHERE datname='faas' GROUP BY state"
```

A healthy apid writes many events rows per second (every auth.*
emission). A drop to ~0 with sustained `apid_audit_write_failures_total`
is the canonical sign that Postgres is rejecting inserts.

### The `__other__` caveat (issue #278)

The account-label admission set is bounded (10 000 ids). A customer
whose first failure pushes the set past the cap lands in
`account_id="__other__"` and the original id is **not** preserved on
the metric label. To recover the id, grep the daemon slog for the
WARN line — `subject` carries the original id:

```bash
journalctl -u apid --since '-15m' --no-pager | grep '__other__'
```

If the warning volume is high, also check the admission set's
contents via `pprof` (or simply count distinct ids in the slog over
the window). The cap was sized for a 10k-customer fleet; a
sustained `__other__` rate means the cap needs lifting — file an
ADR before doing so, since raising the cap is a Prometheus
cardinality decision (CLAUDE.md "new quota/limit → add to
pkg/api/limits.go").

### The `anonymous` label

`account_id="anonymous"` means the audit emit was system-fired (cron,
account-deletion scheduler, etc.) and no principal was available. It
is **not** a customer-attributable failure; treat it as platform-side
observability debt.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasApidAuditWriteFailures' \
  --duration=30m \
  --comment='investigating apid audit persistence failures'
```

## Recover

The alert clears automatically once the rolling 5m rate falls below
5/min and the `for: 5m` window expires. Recovery actions, in order:

1. **Confirm the metric is zero**:
   `curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=apid_audit_write_failures_total'`
2. **Confirm new audit rows are landing** for a known successful
   action (e.g. the customer's next API key mint should produce an
   `auth.*` event row visible in `pg_get_recent_events`).
3. **Confirm `apid_request_failures_total{account_id=...}` is not
   spiking** for the same customer — if it is, the customer's
   request stream is also failing and the audit failures are a
   symptom, not the cause.
4. **ADR-035 best-effort policy**: audit rows missing for the alert
   window are **not** retroactively backfilled. The customer-facing
   audit log (`GET /v1/audit-events`) simply has a gap. Do not
   promise the customer a backfill — it is not part of the contract.

For sustained platform-wide failures, follow the standard Postgres
recovery runbook (`docs/ops/`); the most common root causes are WAL
replication lag, connection-pool exhaustion under burst load, or
the `events` table bloat (it is append-only and never trimmed
unless a retention ADR is in place — see §17 G3).
