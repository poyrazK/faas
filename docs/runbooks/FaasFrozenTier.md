# FaasFrozenTier

Alerts for the ADR-1151 pause tiers: the frozen tier (schedd freezes an idle
instance in place at its idle timeout and thaws it for the next request) and
quiet pause (vmmd pauses a VM's vCPUs between requests). All of them are
warnings. The frozen tier is a cache over parking: whatever goes wrong, the
instance parks and the request restores from the deployment's snapshot
(ADR-005), so these alerts mean slower wakes or wasted memory, not lost
requests, except `FaasQuietResumeErrors`.

Dashboard: **faas frozen tier and quiet pause (ADR-1151)**
(`deploy/grafana/frozen-tier.json`). Switches: schedd `frozen_tier` and
`frozen_tier_apps` (`FAAS_FROZEN_TIER`, `FAAS_FROZEN_TIER_APPS`), vmmd
`quiet_pause` (`FAAS_QUIET_PAUSE`); all off by default.

```sh
curl -fsS http://127.0.0.1:9103/metrics | grep -E '^schedd_frozen_'
curl -fsS http://127.0.0.1:9104/metrics | grep -E '^(vmmd_(freeze|thaw)_seconds|node_frozen_|vmmd_quiet_)'
journalctl -u faas-schedd --since '30 minutes ago' --no-pager | grep -E 'frozen|freeze|thaw'
journalctl -u faas-vmmd --since '30 minutes ago' --no-pager | grep -E 'thawed|freeze|quiet'
```

Turning a switch off is always safe: with `frozen_tier` off, no new
instances freeze, and frozen ones still thaw for requests and park after
their TTL (10 minutes). Do not roll schedd back to a release without the
frozen state while `schedd_frozen_instances` is above 0.

## FaasFrozenThawFailing

More than 10% of thaws, at least three, failed on one schedd in 15 minutes.
Each failed thaw parks the frozen VM and the request restores from the
snapshot.

- `journalctl -u faas-schedd | grep 'frozen thaw failed'` gives the instance
  and the vmmd error.
- A thaw that runs the app's `after_restore` hook fails when the hook
  fails: if the failures come from one app, its hook is broken (check the
  app's logs), and the platform is behaving as designed.
- `thaw did not verify` or `did not run the app's after_restore hook` means
  the node's vmmd predates the thaw fields: upgrade vmmd before enabling the
  tier on that node.
- Otherwise inspect vmmd's `ThawInstance` errors (Firecracker resume, image
  healthcheck timeouts).

## FaasFrozenFreezeErrors

More than 10% of idle-timeout freeze attempts, at least three, errored on one
schedd in 15 minutes. The instances parked as before; customers are not
affected, but the tier is not freezing.

- `busy` and `ineligible` outcomes are normal and excluded; `error` is a
  failed vmmd `FreezeInstance`.
- `Unimplemented` means the node's vmmd predates the frozen tier: upgrade it
  first, or turn `frozen_tier` off for that schedd.
- Otherwise read the vmmd error (pause, cgroup memory reclaim).

## FaasFrozenMemoryPressureParks

More than half of one schedd's frozen parks in 30 minutes, at least ten,
were memory-pressure evictions. Frozen VMs are destroyed before a request
can thaw them, so they hold memory without saving restores.

- Compare **Frozen memory by node** with the node's measured memory. Large
  apps (`frozen_tier_apps`) hold the most.
- Mitigate by turning `frozen_tier_apps` off on that node's schedd, or add
  capacity. With zswap on compute hosts, frozen memory compresses under
  pressure instead (ADR-906).

## FaasFrozenThawSlow

The p95 thaw on one node exceeded 500 ms over 15 minutes, with at least five
thaws. A thaw normally takes milliseconds.

- Slow thaws come from memory faulting back in after reclaim or swap, or
  from a slow image healthcheck or `after_restore` hook. `thaw_ms` in vmmd's
  `thawed` log line shows which instances.
- Check host swap activity and `node_frozen_resident_bytes`; a node under
  memory pressure reclaims frozen memory more often.

## FaasFrozenNodeNearCap

A node holds at least 900 frozen instances, near `FrozenMaxPerNode` (1000).
At the cap, schedd records `node_full` and idle instances park instead of
freezing.

- Check that TTL parks are happening (**Frozen parks by reason**, `ttl`). If
  not, the reaper is stalled on that schedd.
- A node legitimately holding this many idle instances is a placement
  question; the cap bounds Firecracker processes and jail uids.

## Compressed frozen instances (zswap)

On a host with the `zswap` Ansible role (ADR-906 decision 6), memory
pressure first compresses a frozen VM into memory-only swap before it would
park it. **Frozen compressions by outcome** shows `compressed` (kept frozen),
`insufficient` (freed less than half; parked), `unavailable` (the host has
no zswap setup; parked) and `error`. **zswap pool by node** shows the RAM
the pool takes.

- A compressed VM thaws more slowly while its pages fault back in; expect
  `FaasFrozenThawSlow` on a node that compresses a lot.
- `unavailable` on a host that should have zswap: check
  `systemctl status faas-zswap.service`,
  `cat /sys/module/zswap/parameters/enabled` (`Y`), `swapon --show`, and
  `cat /sys/fs/cgroup/faas.slice/faas-tenant.slice/memory.zswap.writeback`
  (`0`). vmmd opens swap to a frozen VM only when all three hold.
- A running VM never swaps: its scope's `memory.swap.max` is `0`; only a
  frozen VM's is `max`.

## FaasQuietResumeErrors

vmmd failed to resume at least three quiet-paused instances on one node in
10 minutes. Each failure fails or retries the request that arrived.

- `journalctl -u faas-vmmd | grep -i 'quiet'` gives the instance and the
  Firecracker resume error.
- Turn vmmd's `quiet_pause` off on the node (`FAAS_QUIET_PAUSE=0`, restart
  vmmd) to stop new pauses while investigating; paused instances resume on
  their next request or when vmmd stops them.
