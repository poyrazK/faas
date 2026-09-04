# FaasFailedLoginSpike

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `apid_failed_login_total{ip}` and `apid_failed_login_audit_dropped_total`
(apid `/metrics`).
Issue: #286.
Severity: warn.

## Symptom

A single source IP exceeded **20 failed logins/min** over the rolling
5-minute window for 5 m. The metric label is the source IP (or
`"__other__"` if the bounded admission set is full — see *Check*).
Companion metric: `apid_failed_login_audit_dropped_total` rising means
the audit flusher is overflowing and rows are being dropped on the
floor (the auth response is unaffected — the drop is non-blocking).

The canonical credential-stuffing pattern is exactly this: one IP
making many attempts against distinct email addresses. The alert is
the SOC 2 CC7.2 visibility signal — the auth-limit bucket is already
shedding the burst at the middleware layer (the customer-facing 401
still returns normally), so this is an **observation ticket**, not an
outage.

## Verify

```bash
# Fleet-wide rate.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=sum%28rate%28apid_failed_login_total%5B5m%5D%29%29'

# Top-10 IPs by rate.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=topk%2810%2C+sum+by+%28ip%29+%28rate%28apid_failed_login_total%5B5m%5D%29%29%29'

# Raw counter from the apid loopback endpoint.
curl -fsS 'http://127.0.0.1:9101/metrics' | grep -E '^apid_failed_login_total'

# Companion: dropped-on-floor rows (best-effort flusher overflow).
curl -fsS 'http://127.0.0.1:9101/metrics' | grep -E '^apid_failed_login_audit_dropped_total'
```

The metric is incremented per failed login from
`cmd/apid/handlers_auth_login.go` (and the OAuth callback denial
paths at `handlers_google.go` / `handlers_github.go`). The handler
emits the audit row **before** returning the 401 — the metric is
incremented inline, the audit row is dispatched to the async
flusher. The two are independent.

## Check

### Per-IP drill-down (the issue's headline use case)

Replace `IP` with the alerting IP (or `"__other__"` if the alert
labels it that way). The label set is identical in Prometheus and on
the wire, so the value can be pasted verbatim from the alerting
notification:

```bash
IP="<paste from alert>"

# Their failed-login rate over the alert window.
curl -fsS --data-urlencode "query=rate(apid_failed_login_total{ip=\"${IP}\"}[5m])" \
  'http://127.0.0.1:9095/api/v1/query'

# Was the AuthLimit finally throttling them? (companion counter)
curl -fsS --data-urlencode "query=rate(apid_authlimit_rejected_total{ip=\"${IP}\"}[5m])" \
  'http://127.0.0.1:9095/api/v1/query'

# The audit rows themselves (the breach-resistant email hash is
# data->>'email_hash'; the ip is data->>'ip_hash' — the audit row
# records the IP separately, not as a label, so the join is
# ip = data->>'ip', not data->>'ip_hash' which is the email hash).
sudo -u postgres psql -d faas -c "\ Timing on" -c \
  "select count(*), data->>'ip' as ip, data->>'email_hash' as email_hash
   from events
   where kind = 'auth.login.failed'
     and at > now() - interval '10 minutes'
   group by data->>'ip', data->>'email_hash'
   order by count(*) desc
   limit 20;"
```

The `email_hash` field is `sha256(lower(trim(email)))` — see
`pkg/auth/hash.go`. The doc comment on `HashEmail` notes that the
hash is "breach-resistant, not strongly anonymous": a rainbow-table
lookup of common addresses is still possible. The 401 response is
the anti-enumeration oracle — the audit row is the **operator**
oracle, which is the right asymmetry.

### The `__other__` caveat (issue #286)

The IP-label admission set is bounded (`maxIPLabelValues = 10_000`,
`pkg/wire/metrics.go`). A source IP whose first failed-login pushes
the set past the cap lands in `ip="__other__"` and the original IP is
**not** preserved on the metric label. To recover the IP, query the
audit rows directly — the audit row's `data->>'ip'` is the literal
source IP regardless of the label-set cap:

```bash
sudo -u postgres psql -d faas -c \
  "select data->>'ip' as ip, count(*)
   from events
   where kind = 'auth.login.failed'
     and at > now() - interval '10 minutes'
   group by data->>'ip'
   order by count(*) desc
   limit 20;"
```

The `__other__` bucket is the canonical sign of a botnet sweep
(many distinct IPs rotating through). The conditional in the SQL
query above is `data->>'ip'` (the row's actual field), not the
metric label — the row is the source of truth.

### The `anonymous` label

`ip="anonymous"` is **not** emitted by the failed-login path. The
label is reserved for the future where the middleware cannot
extract an IP (loopback, dual-stack weirdness, parsed XFF chain
empty). If you see it, file an issue — the customer-facing path
should always have a real source IP.

### What this is NOT

- **Not an account lockout.** The alert does not block the source
  IP. The middleware AuthLimit bucket is the entry-point blocker
  (it 429s at the configured rate); the alert is the visibility
  surface. Account-lockout ("X failed attempts on account Y →
  423 Locked") is a separate tier-2 feature.
- **Not a SOC 2 false-positive signal.** A real burst will trip
  this. A noisy not-quite-burst (e.g. a single user fat-fingering
  their password 5 times in a minute, then succeeding) will not —
  20/min is well above the human noise floor.
- **Not a substitute for `apid_audit_write_failures_total`.** The
  audit-write failure alert catches the case where the audit row
  itself is missing. The two alerts are paired in the rules file
  for exactly this reason.

### Recovery actions

The alert clears automatically once the rolling 5m rate falls below
20/min and the `for: 5m` window expires. Triage actions, in order:

1. **Confirm the metric is decaying**:
   `curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=rate(apid_failed_login_total[5m])'`
2. **Identify the pattern**:
   - `topk(20, sum by (ip) (rate(apid_failed_login_total[5m])))` —
     single IP dominates → likely targeted attack.
   - `count by (ip) (apid_failed_login_total)` — many IPs each at
     low rate → likely botnet sweep, the `__other__` bucket likely
     active.
3. **Inspect the audit rows** to see the targeted accounts
   (`email_hash` join). If the same `email_hash` appears across
   many IPs, that's the targeted account — notify the customer and
   force a password reset (the dashboard's `/v1/account` surface
   has a `require_password_reset` flag hook; the customer-self-
   reset flow is independent).
4. **If the burst is sustained, escalate the AuthLimit** — the
   bucket config is in `pkg/middleware/authlimit.go`; the
   defaults are 10/min/IP. Raising the cap is a quota/limit
   decision (CLAUDE.md "new quota/limit → add to
   pkg/api/limits.go"); an ADR is required before merging.
5. **If the audit rows are also missing** (`apid_failed_login_audit_dropped_total` rising),
   follow `docs/runbooks/FaasApidAuditWriteFailures.md` — the
   flusher is the bottleneck, not the customer action.

The credential-stuffing runbook playbook at §11 maps to this alert
1:1; the alert is the operator visibility for the SOC 2 CC7.2
"monitor for unauthorized access" control. SOC 2 evidence
collection script: `select count(*) from events where kind =
'auth.login.failed' and at > now() - interval '5 minutes'`.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasFailedLoginSpike' \
  --duration=30m \
  --comment='credential-stuffing actor under manual block'
```

DO NOT silence globally — the alert is the SOC 2 evidence signal.
Per-actor silences are fine; fleet-wide silences are not. If the
alert is noising you, file an issue to tune the threshold (the
20/min/IP/5m figure is in `deploy/ansible/roles/prometheus/files/faas.rules.yml`).
