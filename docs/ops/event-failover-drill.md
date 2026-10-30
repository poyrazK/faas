# Event failover drill

The event GA gate ([ADR 20261029093000](../adr/20261029093000-event-ga-gate.md))
needs proof of what an unplanned control-plane failover does to events. This
drill measures it on disposable databases. The full drill between the
production hosts is in [ControlPlaneFailover.md](../runbooks/ControlPlaneFailover.md#drill).

```bash
PG_BIN=/usr/lib/postgresql/16/bin FAAS_EVENT_DRILL_DIR=out make drill-event-failover
```

It needs PostgreSQL 16 binaries (`PG_BIN`, default: the directory of `pg_ctl`
on `PATH`) and touches nothing outside a temporary work directory.

## What it does

1. Starts a primary and a streaming standby (`pg_basebackup -R`), and
   migrates the primary.
2. Publishes events at `FAAS_EVENT_DRILL_RATE` (default 50/s, the GA target)
   for `FAAS_EVENT_DRILL_CRASH_AFTER_SECONDS` (default 30), recording every
   acknowledged event.
3. Crashes the primary with an immediate stop (no checkpoint, no final WAL
   send) while publishing continues.
4. Promotes the standby and retries a publish until the new primary
   accepts one.
5. Checks every acknowledged event against the new primary, then runs fanout
   sweeps there until every receipt settles, for up to a minute.

## Report

`event-failover-drill.json`:

| Field | Meaning |
|---|---|
| `acknowledged` | publishes that returned success before the crash |
| `survived`, `lost` | acknowledged events present, or missing, on the new primary |
| `loss_window_seconds` | how long before the crash the earliest lost event was acknowledged |
| `crash_to_first_accept_seconds` | from the crash to the first publish the new primary accepted |
| `receipts_settled`, `receipts_unsettled` | the account's receipts on the new primary after routing |
| `routing_settle_seconds` | how long routing took to settle them |

The drill fails if any receipt stays unsettled. Lost events are reported,
not failed: the GA gate accepts a loss window bounded by the standby's
replication lag. On one host the lag is milliseconds, so losses there are
rare, and the window to expect in production is the lag that
`FaasPostgresStandbyLagHigh` watches between the real hosts.
