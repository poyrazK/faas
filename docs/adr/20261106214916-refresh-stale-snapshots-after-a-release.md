# ADR-20261106214916 · Refresh recently active apps' snapshots after a release, unbilled

- **Status:** Proposed
- **Date:** 2026-11-06
- **Amends:** spec §4.4 (snapshot restore) and §4.7 (billing): a new
  platform-initiated, unbilled instance mode. ADR-005's cold-boot fallback and
  ADR-510's exact backing check are unchanged.
- **Related:** [ADR-510](510-snapshot-backing-image-identity.md) (restore only
  onto the images a snapshot was captured with),
  [ADR-20261105150000](20261105150000-guest-init-reuse-keeps-bases-and-snapshots.md)
  (a release keeps guest-init while its code is the same), ADR-125 (mirror
  instances are never billed), ADR-732 (forks do not count toward
  `max_concurrency`).
- **Switch:** schedd runtime config `snapshot_refresh` = `off` | `shadow` |
  `on`. Default `off`.

## Context

A release replaces the shared read-only base (drive0) on each node whenever
guest-init's code changed, and a Firecracker or kernel upgrade changes the
backing too. ADR-510 then refuses every snapshot captured on the old images,
correctly: restoring old RAM onto a new base killed interpreters in
production. The app's first wake after the release cold-boots, and its next
park captures a snapshot on the new base.

What that costs users (production, 7 days to 2026-11-05, 528 gateway wakes):

- 14% of user wakes fell back from snapshot restore to cold boot.
- 205 of the 207 fallbacks were `snapshot backing images changed` after a
  release.
- A cold boot took 3.9 s at the median. A restore takes about 0.3 s
  (restore p50/p95 272/304 ms on the reference bench).

ADR-20261105150000 keeps the base when guest-init's code is unchanged, but
only 1 of the last 5 release pairs was that case. Every release that changes
what the guest runs still invalidates every snapshot, and the first user
request to each app pays the cold boot.

## Decision

After a node takes a release, schedd re-captures the snapshots of recently
active apps on that node in the background, before their users ask for them.
The customer is not billed for this work.

1. **What starts a pass.** A node's `compute_nodes.release_id` changes (the
   release install stamps it), and once per node when schedd starts, since
   schedd restarts during a release. schedd runs one refresh pass for that
   node. A pass for a base, kernel or Firecracker change made outside a
   release, started by an operator, is a later addition.
2. **Which snapshots.** For each app with a gateway wake in the last
   `snapshot_refresh_window_hours` (default 168), schedd asks the node's vmmd
   whether the app's live snapshot would restore there. A new
   scheduler-only vmmd RPC, `SnapshotBackingCurrent`, runs ADR-510's check
   (recorded `…/backing` identity against the files a wake would attach)
   without starting a VM and answers `current`, `stale` or `unknown`.
   Only `stale` is refreshed. The check stays exact: a pass that asks too
   often costs a lookup, never a wrong restore.
3. **Skipped.** schedd does not refresh:
   - apps with a live instance (they are recaptured after they park);
   - suspended, deleting or dunning-blocked accounts, and paused or deleted
     apps;
   - workers, jobs and apps whose deployment cannot be primed
     (`releaseAllowsPrime`, managed PostgreSQL admission);
   - snapshots already captured after the node's release: the node answers
     `current` for them.
4. **How.** A refresh is the deploy pipeline's prime: cold boot the app's
   current deployment on the node that holds its snapshot, wait for
   readiness, capture and park (`snapshotAndParkPrime`). The new snapshot is
   local to that node, so the next wake restores locally. Replicas upload as
   usual.
5. **Order and pace.** Most recent park first. One refresh at a time per
   node; nodes are passed concurrently. A refresh is admitted only while the
   node keeps the headroom the builder's second slot requires (admitted
   tenant RAM below 60% of the node's ceiling). It never waits for room and
   never outranks a tenant wake: without room it is deferred, keeps its
   stale snapshot, and the node's pass runs again on the next tick.
   The stale row is retired once the refresh is admitted, as a wake whose
   restore fell back retires it; otherwise imaged would keep it over the
   new capture.
6. **A new instance mode, `refresh`.** The refresh VM:
   - is never routed;
   - is not counted toward `max_concurrency`, as forks are not (ADR-732), so
     a user's wake for the same app is never blocked by it;
   - counts toward the node RAM ceiling and admission;
   - is not idle-reaped: the refresh coordinator parks it after the capture,
     or destroys it on failure; one older than 10 minutes, left by a
     scheduler that stopped mid-refresh, is destroyed on the next tick;
   - is not billed (below).
7. **Rollout.**
   - `shadow` logs and counts each pass's candidates and skip reasons without
     booting anything.
   - `on` performs it.
   - Turn `on` for one node across a base-changing release and compare its
     restore-fallback share with the other node before the fleet.

## Billing

A refresh is work the platform causes: the release made the snapshot stale,
not the customer. The customer is not billed for it, as for mirror instances
(ADR-125):

- `capture_instance_billing_interval` treats mode `refresh` like `mirror`, so
  no billing interval opens.
- The meter sampler's mirror skip covers `refresh` too.

The financial model already absorbs platform overhead. A refresh costs one
cold boot (about 1.3–2 s of guest CPU), a capture and an upload for each
recently active app per base-changing release. The cold boot it replaces
would have run anyway at the app's first wake, billed. The net cost is
mostly that unbilled boot, plus a boot for apps that would not have woken
again before their next deploy.

## Security

- **No new privilege or surface.** A refresh boots the customer's own
  deployment through the same jailer, uid, seccomp, cgroup, netns and egress
  rules as any wake, with the same secrets. It never receives external
  traffic.
- **Attribution.** The wake trigger is `platform.snapshot_refresh`. Secret
  delivery records and audit rows carry it, so a boot the customer did not
  cause is labelled as the platform's.
- **The exact check is unchanged.** Refresh only replaces a stale snapshot
  with a fresh capture. Nothing restores an old snapshot onto new images.
- **Account state is respected.** Suspended, deleting or dunning-blocked
  accounts, paused apps and blocked managed databases are skipped (decision
  3).
- **The new RPC is scheduler-only.** `SnapshotBackingCurrent` is served on
  `vmmd.sock` / mTLS under the existing scheduler-only caller policy, never on
  the gateway's local data socket (ADR-20261030230157).

## Invariants (spec §6.2)

1. `max_concurrency`: unchanged for routable instances. A `refresh` instance
   is excluded the way forks are, and is never routable.
2. RAM ceiling: a refresh is admitted and counted like any instance.
3. Live snapshot or cold-bootable rootfs: unchanged. A refresh replaces a
   stale snapshot with a fresh one; on failure the rootfs remains.
4. A parked app holds zero resident RAM: the refresh VM parks or is
   destroyed, and its cgroup goes.
5. Unique IP, netns, jail uid and RNG stream: a refresh is an ordinary boot.

The property tests gain `refresh` instances in their generators.

## Consequences

- After a base-changing release, users' first requests to recently active
  apps restore (about 0.3 s) instead of cold-booting (3.9 s median), for
  every app a pass reaches before its first post-release wake. An app woken
  before the pass reaches it still cold-boots once, as today.
- Apps run their startup code once more per base-changing release. That boot
  happens anyway at the next wake today; the refresh moves it earlier. It
  shows in app logs and the timeline as a platform snapshot refresh, and the
  customer docs say so.
- Nodes spend a bounded amount of background CPU after each release. It is
  paced below tenant work.
- Complementary to ADR-20261105150000: when guest-init's code is unchanged
  the base is kept, `SnapshotBackingCurrent` answers `current`, and a pass
  boots nothing.
- New pieces:
  - the `refresh` mode (migration: the instances mode CHECK and the billing
    interval function, expand-only, `lock_timeout`);
  - one additive vmmd RPC;
  - a schedd refresh coordinator;
  - two runtime config keys, `snapshot_refresh` and
    `snapshot_refresh_window_hours` (1 to 720, inside the 30-day retention
    of the parked rows the window is read from), with defaults in
    `pkg/api/limits.go`.

## Metrics

- `schedd_snapshot_refresh_total{outcome}`, one per candidate app per pass,
  with outcome in `current`, `would_refresh` (stale; counted by a shadow
  pass), `refreshed`, `deferred` (no headroom), `failed`, `unknown`,
  `no_snapshot`, `skipped_live` and `skipped_ineligible`.
- `schedd_snapshot_refresh_pending{node}`: stale snapshots the node's last
  pass found and the platform has not refreshed yet.
- The existing restore-fallback reason counter shows the effect:
  `snapshot_stale` fallbacks after a release should fall toward zero for
  refreshed apps.

## Rejected alternatives

- **Keep cold-booting on first wake (today).** That is 14% of user wakes at
  3.9 s after every base-changing release.
- **Keep old base generations on disk so old snapshots restore onto them.**
  Restored apps would keep running the previous guest-init until their next
  deploy, so guest-init fixes, including security fixes, would not reach
  them. It also keeps about 150 MB per runtime per generation on every node.
- **Make guest-init change less often** (a guest-protocol package carved out
  of `pkg/api`). Worth doing and complementary, but it reduces how often
  snapshots go stale. It does not help the releases that really change guest
  code, or Firecracker and kernel upgrades.
- **Bill the refresh.** The customer did not cause it. Spec §4.7 bills a
  customer's instances by running seconds, and mirror instances, which
  serve no customer traffic, are already excluded the same way.
- **Refresh every app with a snapshot.** That boots apps nobody uses, with
  their startup side effects and their cost. The recent-wake window bounds the
  work to apps whose users will likely return.

## Verification

- **schedd:**
  - selection and skip rules;
  - ordering by recent wake;
  - pacing and preemption by tenant wakes;
  - `refresh` excluded from `max_concurrency` and routing, counted for RAM;
  - parked or destroyed on every exit;
  - `shadow` boots nothing.
- **Postgres tests:** no billing interval opens for mode `refresh`.
- **vmmd:** `SnapshotBackingCurrent` answers `current`, `stale` and
  `unknown` exactly as the restore check would; it is refused on the local
  data socket.
- **Metal:** replace a node's base. A refresh pass captures on the new base,
  and the next wake restores instead of cold-booting. `make leakcheck` stays
  clean.
- **Production:** one node `on` across a base-changing release. Compare its
  restore-fallback share and cold-boot p50 with the other node.

## Open questions

- Should customers be able to opt an app out of platform refreshes, for apps
  whose startup has side effects they do not want repeated?
- Is 168 h the right window? Shadow mode's candidate counts per release will
  tell how many boots it implies.
