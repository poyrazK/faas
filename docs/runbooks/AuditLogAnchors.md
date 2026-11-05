# AuditLogAnchors

Alerts: `FaasAuditLogTampered` (page), `FaasAuditAnchorErased` (page),
`FaasAuditAnchorStale` (warn), in `pg_backup.rules.yml`, family
`audit_anchor`.
Units: `faas-audit-anchor.timer` (every 15 minutes) and
`faas-audit-verify.timer` (daily) on the control plane, role `audit_anchor`.
Metrics (textfile collector): `faas_audit_anchor_seq`,
`faas_audit_anchor_recent_findings`,
`faas_audit_anchor_last_success_timestamp_seconds`,
`faas_audit_verify_findings`, `faas_audit_verify_last_success_timestamp_seconds`.
Decision record: [ADR-20261026163015](../adr/20261026163015-audit-log-anchors.md).

## How it works

Every 15 minutes `gregalectl audit anchor` seals the audit rows received in
the next window of time into a row in `audit_log_anchors`: the row count, a
digest over the rows, and an HMAC chained to the previous anchor. The HMAC
key is derived from apid's audit HMAC key, which is never in the database.
Each run logs the anchor as an `audit anchor sealed` line, which the journal
ships to Loki, and re-verifies the last 48 hours. `gregalectl audit verify`
checks the whole chain daily.

## Verify

```bash
# The whole chain, now (exit 0 clean, 1 findings, 3 error)
sudo gregalectl audit verify --db-env /etc/faas/sealed.env
sudo gregalectl --json audit verify --db-env /etc/faas/sealed.env

# What the runs reported
journalctl -u faas-audit-anchor.service -u faas-audit-verify.service --since -1d --no-pager | grep 'audit anchor'
```

The off-host copy of every anchor, in Grafana Explore on Loki:

```logql
{job="faas-journal", unit="faas-audit-anchor.service"} |= "audit anchor sealed" | json
```

## FaasAuditLogTampered

Sealed audit rows no longer match their anchor, or an anchor no longer
matches its MAC. Each `audit anchor finding` line names the anchor and kind:

| Kind | Meaning |
|---|---|
| `rows_changed` | A row in the window was rewritten, erased, or added with a `received_at` inside it. |
| `mac_mismatch` | The anchor row itself was changed or forged. |
| `chain_broken`, `missing_anchor`, `window_gap` | Anchors were deleted or edited, so the chain no longer links. |
| `key_unknown` | The anchor was sealed under another audit HMAC key (see Stale, key changed). |

Treat it as a security incident:

1. Preserve the evidence before anyone changes anything: dump the window's
   rows and the anchors.

   ```bash
   sudo -u postgres pg_dump -d faas -t audit_log -t audit_log_anchors -Fc -f /root/audit-incident-$(date -u +%Y%m%dT%H%M%SZ).dump
   ```

2. Find the window: the finding's anchor seq gives `window_start` and
   `window_end`. The anchor's off-host `audit anchor sealed` line gives the row
   count and digest at sealing time.
3. Recover the original rows from the newest PostgreSQL basebackup taken
   after the window was sealed (`docs/runbooks/PostgresBackup.md`): restore
   it to a scratch instance, never over production, and compare that
   window's rows with production.
4. Rotate the database credentials that can write `audit_log`, and review
   who held the schema owner `faas` or a superuser during the window.
5. A single `rows_changed` on the newest windows with no other finding can
   be a writer that stamped a row more than about 10 minutes before it
   committed. Confirm from the row itself before calling it benign.

## FaasAuditAnchorErased

`faas_audit_anchor_seq` fell below its 7-day maximum: the newest anchors are
gone from the database.

1. If the control-plane database was just restored to an earlier point
   (`ControlPlaneStateBackup.md`, `ControlPlaneFailover.md`), this is
   expected: the restore took the newer anchors and rows with it. Check that
   the database's newest anchor matches the `audit anchor sealed` line Loki
   shows for that seq (same MAC). The next run continues the chain. Silence
   the alert for 7 days.
2. Otherwise anchors were deleted, most likely with the audit rows they
   sealed. Handle it as tampering above. Loki has the deleted anchors'
   windows, counts and digests; the basebackups have their rows.

## FaasAuditAnchorStale

No anchor run completed in an hour, or no full verification in 26 hours.
Rows received meanwhile are not sealed. `journalctl -u faas-audit-anchor.service`
shows why:

- **`unknown subcommand "anchor"`**: the installed `gregalectl` predates the
  release that ships anchors. Install the release.
- **Database or key errors**: `--db-env /etc/faas/sealed.env` must hold
  `DATABASE_URL`, and the audit HMAC key must be in `FAAS_AUDIT_HMAC_KEY` (in
  that file) or `/var/lib/faas/audit-hmac.key`.
- **`the newest anchor does not verify under this key`**: the newest anchor
  was altered. The run refuses to extend a forged chain. Handle it as
  tampering above.
- **`the newest anchor was sealed under another key`**: apid's audit HMAC
  key changed. After a failover, the standby must have the primary's key
  (`ControlPlaneFailover.md`, "Before you need it"); install it and the runs
  resume. Only if the key is truly lost, record the incident, then continue
  the chain under the new key once:

  ```bash
  sudo gregalectl audit anchor --db-env /etc/faas/sealed.env --accept-key-change
  ```

  The anchors sealed under the old key then verify only as `key_unknown`;
  run later verifications with `--from-seq` set to the first new anchor.

## Silence

```bash
amtool silence add alertname=FaasAuditAnchorErased --duration=168h \
  --comment='control-plane database restored to an earlier point'
```
