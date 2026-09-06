# RAM-pressure eviction — operator procedure

Source of truth: `pkg/sched/reaper.go::SelectEvictions` (policy),
`pkg/sched/loop.go::runReaper` (trigger), `pkg/sched/engine.go::Evict`
(execution). Spec §4.3 (eviction), §6.2 invariant 2 (the RAM ledger),
§12 (`fcvm_resident_ram_pct`).

Paired alert: [`FaasHighResidentRam`](../runbooks/FaasHighResidentRam.md).
That alert is the *leading* indicator; by the time eviction fires the
box is already shedding customer instances.

## The one thing to know first

**An evicted instance goes to `STOPPED`, not `PARKED`.** No snapshot is
taken on the eviction path (`Engine.Evict` calls `timedDestroy` directly,
then transitions to `StateStopped`). The customer's next request is
therefore a **cold boot**, not a snapshot restore — seconds, not the
≤ 350 ms p50 wake budget.

That is the customer-visible symptom: an app that was fast becomes slow
once, with no deploy and no error. Eviction is silent from the
customer's side and — see *Verify* — nearly silent from ours.

## Trigger

`runReaper` ticks every 10 s and calls `SelectEvictions(resident, now, snapshot)`.
Eviction is a no-op unless:

```
residentMB > EvictionThresholdMB
```

`EvictionThresholdMB = api.RAMAdmissionCeilingMB * 80 / 100` =
**38,080 MB** — 80 % of the 47,600 MB admission ceiling, which is itself
85 % of the 56 GB tenant budget (spec §1). Resident MB is summed as
`api.BillableRAMMB(ram_mb)` = plan RAM **+ 8 MB** per instance, matching
the admission ledger.

So: admission stops granting at 47,600 MB; eviction starts shedding at
38,080 MB. Eviction is not a last resort — it runs well below the
ceiling, by design.

## Selection order

Candidates are filtered, then sorted, then evicted greedily until
`remaining <= 38,080 MB`.

**Filter** — an instance is a candidate only if both hold:

1. `State == RUNNING`. Parked, waking, cold-booting, snapshotting and
   stopped instances are never eviction targets.
2. `now - Started >= MinInstanceAge` (**30 s**). This is the cold-boot
   carve-out: an instance that just woke has not yet served the request
   that woke it, and evicting it would produce a wake→evict→wake loop
   that never converges.

**Sort** — three keys, in order:

| Key | Rule | Why |
|---|---|---|
| 1 | non-Scale before Scale | Scale-plan tenants are evicted **last** (spec §4.3) |
| 2 | oldest `LastRequest` first | LRU — the least recently used instance is the cheapest to lose |
| 3 | instance id ascending | determinism, so a replayed tick evicts the same set |

**Greedy drain** — walk the sorted list, evicting until projected
resident drops to the threshold. Each eviction credits
`BillableRAMMB(ram_mb)` back.

Consequence worth internalising: a single Scale tenant is evicted only
after **every** Free/Hobby/Pro instance old enough to qualify has already
gone. If the box is dominated by one Scale customer, eviction may free
very little before it reaches them.

## Verify — which tenant got evicted, and why

There is an observability gap here. Be aware of it before you start
grepping:

- **A successful eviction emits no log line and no metric.** The only
  `slog` call on the path is the *failure* branch
  (`reaper: eviction`, level WARN, in `loop.go`).
- There is **no** `eviction_fired_total` counter and **no**
  `gateway_wake_rejections_total` counter in the codebase today. If a
  dashboard or a runbook references either, it is aspirational — do not
  build a query on them. (Tracked as issue #255 acceptance item 5.)

What you *can* observe:

```bash
# 1. Is the box above the eviction threshold right now?
#    38080 / 47600 = 80% — the gauge is a percentage of the ceiling.
curl -fsS http://127.0.0.1:9103/metrics/fcvm | grep fcvm_resident_ram_pct
```

```bash
# 2. Which instances were stopped recently? Eviction lands them in
#    'stopped' with no parked_at. A reaper park lands in 'parked'.
sudo -u postgres psql -d faas -c "
  SELECT i.id, i.app_id, a.slug, a.plan, i.ram_mb,
         i.started_at, i.last_request_at
  FROM instances i JOIN apps a ON a.id = i.app_id
  WHERE i.state = 'stopped'
    AND i.started_at > now() - interval '30 minutes'
  ORDER BY i.last_request_at ASC;"
```

The ordering of that result should mirror the *Selection order* table —
non-Scale first, oldest `last_request_at` first. If it does not, the
policy and the observed behaviour have diverged and that is a bug worth
a ticket.

```bash
# 3. Eviction FAILURES (the only logged branch).
journalctl -u faas-schedd --since '-30m' --no-pager | grep 'reaper: eviction'
```

A `reaper: eviction` WARN means `Engine.Evict` returned an error —
usually a `timedDestroy` timeout against vmmd. Those instances are
**still resident**, so pressure will not drop and the reaper will retry
on the next 10 s tick. Repeated identical lines for one instance mean a
wedged VM, not an eviction problem: see
[`FaasDaemonDown`](../runbooks/FaasDaemonDown.md) for vmmd triage.

```bash
# 4. Confirm the ledger agrees with reality (invariant §6.2-2).
sudo -u postgres psql -d faas -t -A -c "
  SELECT COALESCE(SUM(ram_mb + 8), 0)
  FROM instances
  WHERE state IN ('waking','cold_booting','running','snapshotting');"
# Compare against 47600. Above 38080 → eviction is active.
```

## Recover

Eviction is the system working, not failing. The operator question is
never "how do I stop it" but "why is the box this full".

1. **Confirm it is real pressure, not a leak.** Query 4 above sums the
   ledger from `instances`. If that sum is far below what
   `fcvm_resident_ram_pct` reports, the gauge or the ledger has drifted —
   check for instances stuck in `snapshotting` or `waking` past their
   §6.1 timers (WAKING ≤ 5 s, SNAPSHOTTING ≤ 20 s). A stuck row holds
   ledger space without holding RAM.

2. **Identify the dominant consumer.**
   ```bash
   sudo -u postgres psql -d faas -c "
     SELECT a.plan, count(*) AS live, SUM(i.ram_mb + 8) AS mb
     FROM instances i JOIN apps a ON a.id = i.app_id
     WHERE i.state IN ('waking','cold_booting','running','snapshotting')
     GROUP BY a.plan ORDER BY mb DESC;"
   ```

3. **Shed load deliberately rather than letting LRU choose.** Options,
   least to most disruptive:
   - Lower `idle_timeout_s` on the noisiest apps so the idle reaper
     parks them (parking snapshots; eviction does not).
   - Reduce `max_concurrency` on an over-fanned app — the aggressive
     reaper (ADR-038) then parks the surplus within ~30 s.
   - For a single abusive tenant, follow
     [`tenant-abuse`](../runbooks/tenant-abuse.md) — rate-limit first.

4. **If pressure is structural, it is a capacity decision, not an
   incident.** The 47,600 MB ceiling is the financial model's number
   (spec §1). Raising it is a spreadsheet change first and an ADR
   second — see `docs/scale_out_and_workload_classes.md` for the
   vertical-then-horizontal staging.

## Do not

- **Do not raise `EvictionThresholdMB` to make the alert quiet.** It is
  derived (`RAMAdmissionCeilingMB * 80 / 100`); changing it changes when
  the box starts protecting itself, and the 8.4 GB of headroom above the
  admission line (spec §13) exists to absorb spikes, not to be spent.
- **Do not evict by hand while the reaper is running.** `Engine.Evict`
  takes the per-app lock and re-checks `RUNNING` under it; a manual
  `DELETE` or a direct vmmd call races the ledger and will leave
  `ledger.Release` un-called, permanently leaking ledger space until
  schedd restarts.
- **Do not treat a `stopped` instance as lost data.** The platform is
  stateless by contract (`docs/storage.md`); the app cold-boots on the
  next request. Customer *state* was never on the box.

## Known gaps

Tracked under issue #255:

- No `eviction_fired_total{plan,reason}` counter — successful evictions
  are invisible to Prometheus (acceptance item 5).
- No audit event on the eviction path. The aggressive reaper emits
  `events.kind='reaper_scale_down'` (ADR-038); eviction emits nothing,
  so there is no per-app forensic trail.
- No `make eviction-dryrun` rehearsal target (acceptance item 3).

Until the counter exists, the SQL in *Verify* is the only after-the-fact
record, and it is bounded by how long those `stopped` rows survive.
