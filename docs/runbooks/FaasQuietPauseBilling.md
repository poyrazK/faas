# FaasQuietPauseBilling

Alerts for unbilled quiet pause (ADR-20261023171905). vmmd quiet-pauses a
`between_requests` VM 2 s after its last request (ADR-1151); with this feature
the paused seconds are not billed. schedd records the pauses each node reports
in the pause ledger (`instance_quiet_pauses`, with a per-node watermark in
`quiet_pause_watermarks`), and meterd subtracts the paused seconds the ledger
proves from each instance-minute's billed residency.

The design fails toward billing: a closed pause counts exactly, an open pause
counts only up to its node's watermark, and anything missing is billed as
running time, as before the feature. So both alerts mean customers paid for
pause time they should not have, never that billing was skipped.

## Check

Switches, all off by default:

- schedd `quiet_pause_ledger` (`FAAS_QUIET_PAUSE_LEDGER`): write the ledger.
- meterd `[meter] quiet_pause_unbilled` (`FAAS_QUIET_PAUSE_UNBILLED`): `off`,
  `shadow` (count what would be subtracted in
  `meterd_quiet_pause_unbilled_seconds_total{mode="shadow"}`, bill as before)
  or `on` (subtract). Turning it off restores full billing from the next
  rolled minute; rolled minutes stay as they were.

```sh
curl -fsS http://127.0.0.1:9103/metrics | grep -E '^schedd_quiet_pause_ledger_'
curl -fsS http://127.0.0.1:9105/metrics | grep -E '^meterd_quiet_pause_'
journalctl -u faas-schedd --since '30 minutes ago' --no-pager | grep 'quiet pause ledger'
```

## FaasQuietPauseLedgerWriteErrors

schedd could not write its node's pauses (`outcome="error"`) or read back the
node's open pauses after a restart (`outcome="load_error"`). Until it can, the
node's watermark stays where it was, and meterd bills its open pauses as
running time. Pending pauses stay in memory and are written on the next
success, but a schedd restart in the meantime loses them; those pauses are
billed.

1. Check schedd's log for `quiet pause ledger: record` or `load open pauses`
   and the error.
2. Check Postgres reachability and connection headroom from the node
   (`FaasPostgresConnectionsHigh`, `FaasDBPoolStarved`).
3. If the ledger itself is broken, turn off `quiet_pause_ledger` on the node.
   Billing then counts that node's open pauses only up to the old watermark,
   which is safe.

## FaasQuietPauseUnprovenMinutes

meterd rolled instance-minutes whose open pause ran past its node's
watermark, so it billed the uncovered part. Normal watermark lag is a few
seconds, against meterd's 60-120 s roll delay, so a sustained count means a
node stopped writing.

1. Find the node: `time() - schedd_quiet_pause_ledger_watermark_seconds` is
   large for it. A node that reports no VM at all also stops moving its
   watermark; that is harmless, because it has no pauses to prove.
2. Follow FaasQuietPauseLedgerWriteErrors for that node's schedd, or check
   that its capacity stream from vmmd is connected (schedd log
   `capacity stream`).
3. The over-billed minutes cannot be re-rolled: `usage_minutes` keeps the
   first value. If the incident was large, credit affected accounts with the
   `meterd_quiet_pause_unproven_minutes_total` window as evidence (spec
   §4.7.1).

## FaasQuietPauseUnbilledExceedsMeasured

The other direction from the alerts above: over 6 h, meterd counted more
quiet-paused seconds (`meterd_quiet_pause_unbilled_seconds_total`, subtracted
in `on` mode, only counted in `shadow`) than vmmd measured on the nodes
(`schedd_quiet_pause_measured_seconds_total`), by more than 5% plus 10
minutes. meterd floors paused seconds per instance-minute and bills missed
pauses, so it normally reads a little under the measurement. Above it, the
ledger proves pauses that did not happen: customers are under-billed in `on`
mode, or would be in `shadow`.

1. Stop the loss first: set meterd's `quiet_pause_unbilled` to `shadow`
   (`FAAS_QUIET_PAUSE_UNBILLED=shadow`). Billing returns to running time from
   the next rolled minute; the alert keeps evaluating in shadow.
2. Compare the two series per node to find where the excess comes from:
   `sum by (node) (increase(schedd_quiet_pause_measured_seconds_total[6h]))`.
   A node missing from it while its schedd writes the ledger points at that
   schedd's capacity stream or a vmmd without the pause record fields.
3. Look for open pauses that never close:
   `SELECT node_id, count(*), min(started_at) FROM instance_quiet_pauses WHERE ended_at IS NULL AND started_at < now() - interval '1 hour' GROUP BY 1;`
   An open pause counts up to its node's watermark, so one whose VM is gone
   keeps adding unbilled time for every minute the instance id runs again.
4. Check clock skew between compute nodes and the control-plane database
   (`chronyc tracking` on both). Pause times are vmmd's wall clock; billing
   intervals are the database's.
5. Keep `shadow` until the cause is fixed and the series agree for a full
   day.
