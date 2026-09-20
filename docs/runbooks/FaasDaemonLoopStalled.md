# Daemon loop stalled

`FaasDaemonLoopStalled` fires when a daemon's main loop has not reported
progress within its budget (ADR-190). The daemon is usually still `up`
and often still `ready`: it answers `/metrics` and `/readyz` while the
goroutine that does the work is blocked. This is the failure shape of
the 2026-09-03 schedd prime wedge, where a `PauseAndSnapshot` RPC with no
deadline held the notify goroutine for ten minutes.

## What happens on its own

Every `Type=notify` unit sets `WatchdogSec`. The daemon sends `WATCHDOG=1`
only while every registered loop is within budget, so a stalled loop stops
the pings and systemd kills and restarts the unit after `WatchdogSec`
(`Restart=on-failure`). Expect `<daemon>_daemon_restart_count_total` to
increment and `journalctl -u faas-<daemon>` to show `Watchdog timeout`.

| Daemon | Loop | Budget | WatchdogSec |
|---|---|---|---|
| schedd | `main` (notify + tick select) | 180 s | 180 s |
| vmmd | `sweep` (parent-mount sweep) | 3 × sweep interval (90 s) | 120 s |
| every notify daemon | `runtime` (1 s ticker) | 30 s | per unit |

## Triage

1. Identify the loop and daemon from the alert labels, then read the age:

   ```promql
   {__name__=~".+_loop_last_beat_age_seconds"}
   ```

2. If the unit has not restarted yet, capture a goroutine dump before it
   does. The stack of the blocked loop is the whole diagnosis:

   ```bash
   sudo kill -QUIT "$(systemctl show -p MainPID --value faas-schedd)"
   sudo journalctl -u faas-schedd --since -5m | grep -A200 'goroutine '
   ```

3. Look for the blocked call. Typical causes, in order of frequency:
   - a gRPC call without a deadline (should now be bounded by
     `FAAS_GRPC_DEFAULT_DEADLINE`; check
     `<daemon>_grpc_client_calls_without_deadline_total`);
   - a synchronous notify handler doing long work (Prime on schedd);
   - a Postgres statement waiting on a lock or a full connection pool
     (`pg_stat_activity` on fsn-1).

4. If the same loop stalls repeatedly after restart, the cause is
   deterministic. Stop the restart cycle with `systemctl stop`, fix the
   blocking call, and open a fix PR with a regression test.

## False positives

- A `runtime` stall on a host under memory pressure or a long GC pause.
  Check `node_memory_MemAvailable_bytes` and `go_gc_pauses_seconds` first.
- A schedd `main` stall shorter than three minutes during a large Prime is
  within budget and does not fire; if it does fire, the Prime exceeded
  both `ColdBootTimeout` and the snapshot budget and is itself the bug.

## Related

- `FaasDaemonDown`, `FaasDaemonNotReady`: the other two axes (process
  gone, dependencies down). This alert is the third: process present,
  dependencies fine, no work happening.
- `docs/adr/190-daemon-durability-primitives.md`
