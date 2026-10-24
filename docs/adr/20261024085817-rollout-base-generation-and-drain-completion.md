# ADR 20261024085817 · A compute rollout never strands a node on a runtime base it cannot boot, and its drain finishes

- **Status:** proposed (2026-10-24)
- **Date:** 2026-10-24
- **Related:** ADR-005 (cold boot always works), ADR-137 (recovery arbiter),
  ADR-510 (snapshot backing identity), ADR-567 (runtime base convergence),
  ADR-736 (immutable runtime releases), ADR-1151 (frozen tier), ADR-1740
  (frozen fast path), [release compatibility](../release-compatibility.md)
- **Incident:** production-us, 2026-10-22 15:32–15:40 UTC, rc.290 full
  release (cd-platform run 39790744596, attempt 1)

## Context

### What happened

The rc.290 release changed two runtime base generations: `base-minimal`
(`db529ed…` → `5ca69ea…`) and `python313`. It ran in `full` mode, one
compute node at a time.

| UTC | Event |
|---|---|
| 14:35–14:59 | fsn-2 rolls to rc.290. Its pre-stage publishes `5ca69ea…` to the shared logical key `base/base-amd64.ext4`. |
| 15:14:46–15:19:39 | fsn-3's play runs the **candidate** (rc.290) imaged pre-stage while fsn-3 still serves on rc.288. |
| 15:20:11 | fsn-3 drain begins. |
| 15:20–15:32 | `drain-status` prints `instances still on fsn-3.faas: 1` for twelve minutes. |
| 15:32:18 | The rescue re-activates fsn-3 on rc.288 and aborts. |
| 15:32–15:40 | Every wake placed on fsn-3 fails 503 `scan_critical` "could not adopt its configured generation". |

### Why fsn-3 refused every wake

Publishing to the shared key did not break fsn-3 by itself. vmmd's
`ensureBaseGeneration` compares the node-local cache marker with the
generation in its process environment. While the marker matches, vmmd never
reads shared storage, and imaged's convergence loop (ADR-567) does not adopt a
publication built from another source ref.

**fsn-3's own pre-stage broke it.** The rc.290 imaged reused fsn-2's verified
producer. `verifiedBaseArtifactReusable` replaced the cached bytes of the
logical key with `5ca69ea…`, and `finishVerifiedBase` marked the cache with
the new generation. From 15:19 the serving rc.288 vmmd saw a marker that was
not its own. It refreshed the scan sidecar, found `5ca69ea…`, and refused every
bring-up on that base: cold boot, restore and migration adoption. Running
guests kept working. The window before 15:20 was short, and the drain then kept
placement away. The rescue put the node back into placement, and the failures
started.

Restoring `/etc/faas/runtime-bases.env` does not help: the generation that
counts is the one committed in the node's cache, and the logical key holds one
generation for the whole fleet.

The same mechanism has two other exposures:

- **Phased mode** (`compute_rollout_mode=phased`, the default) runs the
  pre-stage on every node in parallel during `prepare`, before any node
  activates. Every node then refuses new bring-ups on the changed bases until
  its activation installs the release. That is a fleet-wide cold-boot and
  restore outage for the whole activation phase.
- **No-drain restarts** (vmmd restart adoption) keep placing guests between
  the pre-stage and the vmmd restart.

### Why the drain never finished

The recovery arbiter (ADR-137) handles a gracefully draining node like this:

- **FROZEN:** parked, one tick at a time.
- **RUNNING:** live-migrated. A failed handoff is retried forever, because a
  healthy draining node has no recreate fallback.
- **WARM, WAKING, COLD_BOOTING:** left alone, so a WARM pool VM never leaves.

Elsewhere on the scheduler:

- The ordinary thaw path parks a frozen VM only on an unavailable or
  heartbeat-stale node. A draining node thaws in place.
- The ADR-1740 fast path, warm-pool promotion and the idle freeze never check
  the node's lifecycle.
- `RecoveryRunner.Tick` blocks on a node's migrations, each up to the
  180-second lease. Rows that freeze or thaw during that time wait for the
  next tick.

With the frozen tier on permanently since 2026-10-22 16:36 UTC, the drain
depends on two things. Each running instance's handoff must succeed, and no
request may thaw a VM back into service on the draining node. A frequently hit
app breaks both. On 2026-10-22 one instance whose synthetic check kept it
RUNNING (Hunt 12's `h12-py`) held fsn-3 for the entire 10-minute wait.

## Options weighed

**(a) Do not re-activate after a drain failure when generations no longer
match.** This is necessary but not enough on its own: it leaves the window
between the pre-stage and the drain, the phased `prepare` exposure and the
no-drain path. The useful test is the node-local cache, not the shared key.
The shared key moving does not break a node whose cache still holds its own
generation. A node whose cache moved is broken whatever the shared key says.

**(b) Generation-addressed base keys, so N-1 can always read its own
generation.** This is the complete answer to "release N runs beside N-1": it
also covers a base that is not cached on an N-1 node, and a rollback across a
generation change. It only protects pairs where N-1 already has the code, so it
could not have helped rc.288. It also touches imaged publication, vmmd
adoption, ADR-510 identity, ADR-567 convergence, the verified-producer
catalogue and schedd's base keys, and it needs native acceptance. **Deferred**
to its own ADR (follow-up 1).

**(c) Publish the new generation to the shared key only after every node has
rolled.** Rejected. The first rolled node's vmmd refreshes its configured
generation from the shared key. Without that publication it refuses every
bring-up until the last node rolls, unless the new generation lives under a
second key, which is (b).

**(d) Pre-stage a changed generation behind the drain barrier.** A node's
cache moves to the candidate generation only once the node is empty and takes
no placements. A drain that fails then fails before any base mutation, so
restoring the active lifecycle is safe again. The cost: the first node's
maintenance window grows by the base build (about 5 minutes for rc.290's two
bases), and only on releases that change a generation.

## Decision

Combine (d) with (a) as a backstop, and make the drain bounded.

1. **`vmmd base-generation-check`** (run from the candidate binary, like
   `adoption-check`). It reads the serving vmmd's generations from
   `/proc/<pid>/environ`, the candidate's from `runtime-bases.env`, and each
   base's cache marker through the storage configuration the serving vmmd
   uses. It does not refresh anything. Exit codes:
   - `0`: no generation changes, and the serving bases are intact.
   - `3`: the release changes a generation, and the serving bases are still
     intact.
   - `4`: the cache holds another generation of a base the serving vmmd boots.
     The serving vmmd refuses every guest on that base.
   - `1`: the check itself failed.
2. **`node_join.yml` orders the pre-stage around the barrier.** On exit 3 or
   4 the pre-drain pre-stage is skipped, including in `prepare` (so phased
   preparation no longer touches a serving node's cache). The rollout always
   drains, even when adoption-check would allow a no-drain restart. The
   pre-stage runs after the empty maintenance hold, while the drained node's
   vmmd is still up. The check runs again after `Verify the prepared rollout
   marker`, because `--activate-prepared` starts there.
3. **The rescue re-checks before re-activating.** A node that entered the
   rollout active is restored only when the check answers 0 or 3. Otherwise
   the play leaves the node drained. Its failure names the bases cached in
   another generation (or why the check could not answer), says that
   re-activation would fail every wake with 503 `scan_critical`, and prints the
   last drain status.
4. **A graceful drain has a timeline.** The arbiter reads it from
   `compute_nodes.drain_initiated_at`:
   - For `DrainLiveMigrationWindowSeconds` (120 s), RUNNING instances
     live-migrate as before.
   - From then until `DrainParkAfterSeconds` (window + one 180 s migration
     lease + 30 s of clock skew = 330 s), no new handoff starts. A handoff that
     began inside the window is still in flight then, and no park may race it
     on any schedd.
   - From `DrainParkAfterSeconds`, whatever is still RUNNING gets a new
     `DecisionPark`, which is the ordinary park on its own node.
   - WARM rows park at once.

   Parks run with the migrations' concurrency bound
   (`MigrateLiveConcurrency`). Any schedd may park, including the draining
   node's own, so a single node's drain also completes. The exclusive-snapshot
   barrier and the RUNNING→SNAPSHOTTING compare-and-set keep two schedds from
   parking one row. Force-drain, recovery and unavailable lifecycles keep
   their behaviour. The 600-second drain wait in `node_join.yml` covers the
   park point plus at least two minutes of parks; a contract test pins this.
5. **A node that no longer admits work does not take resident work back.**
   This covers draining, force-draining, maintenance, retired and unavailable
   nodes; the predicate is placement's `IsAdmitting` reversed.
   - The ordinary thaw path parks such a frozen VM (reason `node_draining`).
   - The fast path parks it before thawing.
   - Warm-pool promotion discards a VM on such a node.
   - The idle freeze refuses, and the reaper parks the instance instead.

   The ordinary path uses the node row it already reads. The other three read
   `Engine.ObserveNodeLifecycles`, which the recovery runner feeds with every
   node list it reads, about once a second. That keeps reads off the fast
   path.
6. **`drain-status` names the blockers.** The break-glass path that the
   rollout polls lists every live instance as `app=<slug> instance=<id>
   state=<state>`, up to 20, with the remainder counted. Both failure messages
   include it.

## Consequences

- A release that changes a runtime base generation can no longer strand a
  node on a base it cannot boot through the rollout's own steps. The fsn-3
  failure, the phased `prepare` exposure and the no-drain window are closed.
- A drain on a healthy node finishes within about `DrainParkAfterSeconds`
  plus the parks, whatever the traffic. Busy instances parked after the grace
  pay one wake on an admitting node (a restore, about 0.5 s) instead of a
  handoff. A handoff pauses the guest for its capture anyway, so an in-flight
  request is cut either way.
- New metric labels: `schedd_frozen_parks_total{reason="node_draining"}`,
  `schedd_frozen_fast_path_skipped_total{reason="node_draining"}`, and the
  freeze outcome `node_draining`. Existing dashboards keep working.
- **Residual risks, not closed here:**
  - A base that is **not cached** on an N-1 node is fetched from the shared
    key at its first boot there. After the first node publishes a new
    generation, that boot fails until the node rolls.
  - A compute **rollback** across a generation change leaves the rolled-back
    node with the newer generation cached.
  - The play stages the new `runtime-bases.env` before the drain. An N-1 vmmd
    that restarts in that window starts with the candidate's generations.
  - Option (b) closes all three.
- Native acceptance: the arbiter now dispatches parks, so `make test-metal`
  and `make leakcheck` on a dedicated KVM host are owed before release. The
  macOS development host cannot run them.

## Follow-ups

1. ADR: generation-addressed runtime base keys (option b), so N-1 always reads
   its own generation and rollbacks across a generation change work.
2. apid `GET /v1/compute-nodes/{name}/drain` returns the blocking instances,
   so the non-break-glass `drain-status` can print them too.
3. Release runbook: pause synthetics and crons on test apps before a compute
   rollout. A bounded drain no longer depends on it, but parked test apps
   still mean extra wakes.
