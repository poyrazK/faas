# FaasColdBootRatioHigh

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `vmmd_cold_boot_ratio` (recording rule, derived from
`vmmd_wake_snapshot_tier_total{tier="cold_boot_fallback"}` /
`vmmd_wake_snapshot_tier_total`).
Spec: §6.2 budget (47,600 MB / 56 GB), §12 SLO family
(`snapshot_fleet_avg_mb` 130 plan / 160 warn / 200 page).
ADR: ADR-127 §5 (cold-boot ratio SLO).
Issue: #1059.
Severity: page.

This alert is a SIBLING of `FaasColdBootFallbackHigh` (severity warn,
10 % over 15m) — page tier above 30 % over 10m. The two alerts sit on
either side of the §6.2 budget curve: warn catches rot on individual
apps (FC upgrade lazy re-snapshot), page catches sustained fleet
saturation where 30 % of all wakes are cold-booting and lv-fc
(`/srv/fc`) throughput is approaching the wake-economics limit.

## Symptom

Cold-boot fallback rate has exceeded 30 % of all wakes for 10 minutes.
The fleet has crossed the §6.2 wake-economics saturation point — at
this rate, ~3× the wake-side I/O of a healthy fleet is hitting lv-fc.
Catching the trend at 30 % gives ~24 h headroom before the
`snapshot_fleet_avg_mb > 160 MB` (warn) alert fires and ~12 h before
> 200 MB (page) fires.

## Verify

```bash
# Confirm the alert expression is the one firing.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=vmmd_cold_boot_ratio'

# Direct underlying metric (the denominator).
curl -fsS http://127.0.0.1:9104/metrics | grep -E 'vmmd_wake_snapshot_tier_total'
```

## Check

> **Note on counter semantics** — `vmmd_wake_failure_total` is a
> *per-step* counter (ADR-127 §3.5). The restore-fallback hook fires
> for every restore failure regardless of whether the subsequent
> cold-boot succeeds. A healthy fleet during a transient snapshot-
> stale window can show `snapshot_restore_err` increments without any
> customer-visible wake failures. Use `FaasColdBootRatioHigh` (this
> alert) for customer-visible degradation; use the failure-reason
> panel for per-step triage.

```bash
# Per-reason failure breakdown — the §4.6 / §11 clues that tell us WHY
# the snapshot stopped loading. A {reason="disk_full"} spike is the
# canonical lv-fc exhaustion signal; {reason="snapshot_stale"} is the
# canonical FC-upgrade signal (mutually exclusive for the same
# restore-fallback event).
curl -fsS http://127.0.0.1:9104/metrics | grep 'vmmd_wake_failure_total'

# Fleet snapshot average (the next alert in the chain).
curl -fsS http://127.0.0.1:9104/metrics | grep 'fcvm_snapshot_fleet_avg_bytes'

# Per-box p99 wake latency — use the new ADR-127 per-box histogram.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=histogram_quantile(0.99, sum by (le, box) (rate(vmmd_wake_latency_seconds_bucket[5m])))'

# Recent vmmd slog — line up the alert-firing time with the warn
# cluster to see the underlying error chain.
journalctl -u vmmd --since '-30m' --no-pager | grep -iE 'restore failed|cold boot failed|snapshot.stale|disk.full|ENOSPC'
```

### Reasons-and-triage table

| `reason` | Likely root cause | Operator action |
|---|---|---|
| `snapshot_stale` | Firecracker version bump invalidated snapshots | Lazy re-snapshot (see Recover) |
| `disk_full` | `/srv/fc` lv-fc exhaustion | LV expansion or evict cold snapshots |
| `jailer_fail` | FC binary / seccomp / KVM device / cgroup parent | `systemctl status vmmd` + dmesg |
| `netns_fail` | nft / ip-link / ip-route failure | Check host iptables / nft state |
| `cgroup_fail` | cgroup v2 mount misconfigured | Sustained ⇒ §11 invariant violation |
| `vsock_fail` | Guest-init post-restore resume hook (per ADR-022) | `journalctl -u vmmd` for the vsock log |
| `snapshot_restore_err` | Catch-all bucket — check daemon slog | Read the wrapped error in slog |
| `mem_backend_err` | Should be 0 today (only backend_type=File) | Investigate if non-zero |

## Silence

```bash
amtool silence add \
  --matchers='{alertname="FaasColdBootRatioHigh"}' \
  --duration=1h \
  --comment='FC upgrade completed; lazy re-snapshot in progress'
```

The 1-hour silence window matches the typical lazy re-snapshot window
(ADR-005) — i.e. the alert silences itself after the fleet has
re-snapshotted all apps. A regression that crosses 24 h should be
re-paged.

## Recover

### Path A — `snapshot_stale` after FC upgrade (most common)

ADR-005 says cold-boot always works and lazy re-snapshot self-heals.
Each cold-boot produces a fresh snapshot pinned to the current FC
version. The fleet heals as traffic moves — no operator action unless
the traffic mix doesn't exercise all snapshots within ~24 h. In that
case, manually warm each app:

```bash
# Per-app lazy re-snapshot — kick one wake per app to force the
# regenerate. schedd will pick restore (or cold-boot if no usable
# snapshot), warm the snapshot, and the next wake is restore-rate.
gregale apps deploy --app=<app-id> --image-ref=<latest>
```

### Path B — `disk_full` (lv-fc exhaustion)

```bash
# Inspect lv-fc usage.
lvs /dev/fcvg/lv-fc
# Add 50 GB headroom; the snapshot_fleet_avg_mb alert joins this
# one if the fleet grows.
lvextend -L +50G /dev/fcvg/lv-fc
# Verify the alert clears within the next 5m window.
```

### Path C — `cgroup_fail` or other sustained infrastructure failure

This is the page-tier alert, not the lazy re-snapshot one. A
sustained `cgroup_fail` rate means the host's cgroup v2 mount is
misconfigured (CLAUDE.md invariant §11 requires cgroups v2 with
`memory.max = plan + 8 MB`). Bounce vmmd and verify the cgroup scope
(`/sys/fs/cgroup/system.slice/faas-vmmd.scope`).

## Reference

- Issue #1059 — operator-facing wake-failure observability
- ADR-127 §5 — cold-boot ratio SLO + alert threshold (this runbook)
- ADR-074 — warm-snapshot audit + `*_wake_snapshot_tier_total{tier}`
- ADR-005 — cold-boot always works (warm-tier is cache, never truth)
- §6.2 invariants — RAM admission ceiling + cold-boot budget
