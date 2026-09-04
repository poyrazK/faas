# FaasAuditRetentionExhaustion

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `apid_audit_events_deleted_total` (counter),
`apid_audit_events_retention_lag_seconds` (gauge),
`apid_audit_events_volume_total{kind_prefix}` (counter, labelled).
ADR: ADR-075 (90-day retention floor) + ADR-091 Amendment 2 (D20.3
SLO + observability, PR-B residual).
Severity: page on `audit_events_deleted` absent AND table growing.

## Symptom

The daily audit-event retention cleanup loop (pkg/eventretention,
ADR-075) is failing to keep up. The three signals stack:

- **`apid_audit_events_deleted_total` (counter)**: a 24h rate of zero
  for >48h means the loop is not pruning rows. The counter ticks up
  only when the loop deletes >0 rows in a pass (idle passes don't
  bump it).
- **`apid_audit_events_retention_lag_seconds` (gauge)**: the gauge
  reflects `(now − cutoff)` at the latest successful pass. A
  pinned-zero value means the loop is running but finds nothing to
  prune. The gauge itself reads `~7,776,000 seconds (≈90 days)` on
  every healthy pass — it does NOT grow between passes (the gauge
  is `Set` to `now − cutoff` per `pkg/eventretention/cleanup.go:211-213`,
  and cutoff is fixed at `now − 90d`). Prometheus alerts on
  **staleness** instead: `time() − timestamp(gauge) > 93600` (page,
  `FaasAuditRetentionLoopStalled`) fires when the loop hasn't
  completed a pass in >26h (24h cadence + 2h slack for the first-pass
  grace window). A second alert at `> 180000` (warn,
  `FaasAuditRetentionLoopStretched`) trips when the cadence is past
  2x the interval (50h). A missing/NaN value means the loop never
  completed a pass successfully — escalate.
- **`apid_audit_events_volume_total{kind_prefix}` (counter, labelled)**:
  a divergence between this counter's rate and the table's actual
  growth points to either (a) the loop silently failing (counter
  ticks up; rows don't drop) or (b) the bounded admission helper is
  collapsing volumes to `__other__` (check the label set).

The audit-event table grows ~3-4 GB/year/active-tier through the
auth / key / secret / account / stateless / webhook / edge_rule /
cron emit namespaces (ADR-035). The 90-day floor is the SOC 2 CC6.2
evidence-retention requirement. A 2-day outage of the cleanup loop
adds ~5-10 MB/active-tier/day to the table — small per-customer,
but multiplicative across a fleet. Without intervention the
events table exhausts disk within 2-3 weeks on a steady-tier
single-box.

## Triage

The three signals are designed to compose. Walk top-down:

1. **Is the loop even alive?** Read
   `apid_audit_events_retention_lag_seconds`. If it's present and
   updating, the loop is firing passes — go to step 2. If it's
   missing, the loop has not completed a successful pass since
   boot. Restart apid (`systemctl restart apid`); the loop's
   first-pass-immediate path will fire on boot. If still missing,
   check apid logs for `eventretention.first_pass_failed` (the
   loop's defence-in-depth logs the first-pass error and
   continues) and `eventretention.delete` (the per-pass error
   path).

2. **Is the loop pruning?** Read
   `rate(apid_audit_events_deleted_total[24h])`. A rate >0 means
   the loop is finding rows older than the cutoff and deleting
   them. A rate of 0 with a non-zero growing volume (step 3) is
   the canary — the loop is alive but the cutoff isn't catching
   enough rows. Two possibilities:
   - **Cutoff too far in the future**: someone bumped
     `eventretention.CutoffDays` to 365+ in the apid config
     without authorisation. Reset to 90.
   - **Clock skew**: apid's clock is ahead of the row timestamps
     (the rows are "younger" than the cutoff thinks). Check
     `timedatectl status` on the apid host.

3. **Is the table actually growing?** Compare
   `rate(apid_audit_events_volume_total[24h])` (sum across all
   `kind_prefix` labels except `__other__`) against the rate of
   the table's physical size in `pg_stat_user_tables`:
   ```sql
   SELECT pg_size_pretty(pg_total_relation_size('events')) AS size,
          n_live_tup, n_tup_ins, n_tup_del
     FROM pg_stat_user_tables WHERE relname = 'events';
   ```
   If `n_tup_ins` matches the wire counter but `n_tup_del` is far
   below `n_tup_ins * cutoff_days / interval_days`, the loop is
   not pruning at the rate it's emitting. Two possibilities:
   - **Long-running transaction on the events table** is pinning
     the cleanup's row visibility. Check
     `pg_stat_activity` for `state = 'idle in transaction'`
     sessions with `query_start` > 1h on the events table.
   - **Index bloat** is making the DELETE slow enough that the
     loop fires less often than its 24h interval. Run
     `VACUUM (ANALYZE) events` and `REINDEX INDEX CONCURRENTLY
     events_at_idx` (the cutoff-time index).

## Access control

The audit-events table carries customer identifiers (account_id,
key fingerprints) and — as of ADR-091 Amendment 2 / PR-B — client
IPs (`client_ip` field on `edge_rule.ip_*` rows). Operators with
access to the events table can correlate customer activity with
their network identifiers. This is required for incident response
(SOC 2 CC7.3) but is a privacy surface — see the dashboard scope:

- Production tables: read-only access for SRE on-call; write
  access for the data platform team.
- Staging tables: free read/write.
- IP masking (last-octet truncation for v4, prefix truncation for
  v6) is a separate ADR — not in scope for ADR-091 Amendment 2.

## Verify

The Grafana dashboard `faas-audit-retention-d20-4` (uid
`faas-audit-retention-d20-4`) renders all three signals on one
panel set — open it first; the curl commands below are for ad-hoc
verification when the dashboard isn't available.

```bash
# Is the loop alive? — gauge present + non-NaN AND recent (staleness)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=apid_audit_events_retention_lag_seconds'

# Is the loop running on schedule? — gauge staleness (PR-D20.4 primary signal)
# Healthy: time() - timestamp(gauge) < 86400s (one cadence).
# Page:    > 93600s (26h, one cadence + 2h slack).
# Warn:    > 180000s (50h, 2x cadence + 2h slack).
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=time%28%29+-+timestamp%28apid_audit_events_retention_lag_seconds%29'

# Is it pruning? — counter rate over 24h
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=rate%28apid_audit_events_deleted_total%5B24h%5D%29'

# Volume by kind prefix (top 5)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=topk%285%2C+rate%28apid_audit_events_volume_total%5B5m%5D%29%29'

# Table size + delete/insert rate from postgres
psql -At -c "SELECT n_tup_ins, n_tup_del, pg_size_pretty(pg_total_relation_size('events'))
             FROM pg_stat_user_tables WHERE relname = 'events';"
```

## Escalation

If the loop is alive but the table is on track to exhaust disk in
<7 days:

1. Page the data platform on-call (per `deploy/ansible/.../alertmanager.yml`).
2. Lower `eventretention.CutoffDays` to 60 in the apid config and
   restart. This is a temporary, audit-trail-shortening measure —
   must be paired with a follow-up SOC 2 ticket to extend the
   window once disk is recovered. **Never** lower below 30 without
   security sign-off (the 30-day floor is a hard SOC 2 limit).
3. If disk is the immediate constraint, consider moving the
   events table to a separate tablespace backed by a larger
   EBS volume (`pg_tablespace_location` + `ALTER TABLE events
   SET TABLESPACE events_archive`). Do NOT drop the table —
   audit retention is an audit-trail obligation.

## Related

- `pkg/eventretention/cleanup.go` — the loop driver.
- `pkg/wire/metrics.go` — the three metric registrations.
- `docs/adr/075-event-retention.md` — retention policy ADR.
- `docs/adr/091-edge-rules.md` Amendment 2 — D20.3 SLO scope.
- `deploy/ansible/roles/prometheus/files/faas.rules.yml` — alert
  rules (`FaasAuditRetentionExhaustion`).
