# ADR 20261123122457 · Compute unit lane: vmmd, schedd, bridges and leaf compute daemons ride the unit lane

- **Status:** proposed. The owner chose the three open decisions on 2026-11-23
  (database nodes left pending, stand down on a refused guest, 8–10 min
  for a compute commit).
- **Date:** 2026-11-23
- **Amends:** ADR-1650 (lanes `shared` and `compute`), ADR 20261122170000
  (continuous unit lane)
- **Related:** ADR 20261017 (vmmd restart adoption), ADR 20261017
  (connection survival across data-path restarts), ADR 20261118060000
  (no-drain activation), ADR 20261116191802 (adoption-check retries),
  ADR-062 (per-node schedd), ADR-1027 (database nodes)

## Context

The continuous unit lane ships whole commits only (ADR 20261122170000). A
commit that changes code read only by compute or shared daemons
(`cross_lane`) therefore stalls every later commit until the next full
release.

The last 100 first-parent merges on main (2026-11-20 to 2026-11-23,
`affected_units.py` over each commit and its parent, judged by main's rules)
break down as follows:

| per commit | count |
| --- | --- |
| deploys (control plane, or control plane and edge) | 57 |
| nothing to deploy | 31 |
| blocked | 12 |

The 12 blocked commits break down by blocker as follows. A commit can have
more than one.

| blocker | commits |
| --- | --- |
| vmmd | 3 |
| builderd, imaged and the other leaf compute daemons | 3 |
| guest bridges | 2 |
| deploy/ files | 2 |
| release unit files | 2 |
| migrations | 1 |
| schedd | 1 |

The real cost shows when the lane is replayed in order. A blocked commit
holds every later one until a full release clears it, and the replay counts
each rc tag in the range (eight, rc.366–374) as a deployed full release.
Under those rules, **23 of the 57 deployable commits ship automatically and
34 wait.** Counting each rc tag as deployed is optimistic: rc.374 was
tagged but not deployed for a day.

Most of what a compute lane needs already exists:

- **The lane already reaches compute nodes.** `cd-edge-units.yml` deploys
  gatewayd-internal one node at a time through `cd-edge-node.yml`:
  - a hard-linked overlay;
  - `deployctl deploy --only`;
  - the SLO gate;
  - the watch record;
  - rollback.
- **vmmd restarts without a drain on production.** Both OVH nodes run the
  adoption switches (`vmmd_adoption_canary_nodes`). The full-release restart
  on 2026-11-23 adopted all live guests on both nodes:

  | node | guests adopted | refused | adoption time |
  | --- | --- | --- | --- |
  | bhs-2 | 6/6 | 0 | 73 ms |
  | bhs-3 | 10/10 | 0 | 95 ms |

  It handed the instance bridge units over. The units it stopped belonged to
  guests that were already gone.
- **The bridges need no restart.** vmmd starts each guest's bridge from
  `/opt/faas/current/bin`. On production the bridge runs as a transient unit
  that outlives vmmd. Activating the overlay is enough: new guests start the
  new bridge, and running guests keep theirs until they park.
- **Restarting schedd and builderd is safe.** schedd is per node with no
  leader (ADR-062). builderd drains its builds on stop. No unit is
  `PartOf=` or `BindsTo=` vmmd. Full releases already restart all of them
  node by node, without a drain.

The one missing piece: only `node_join.yml` runs the adoption proof around
a vmmd restart:

1. `vmmd adoption-check` with the candidate binary, with retries;
2. the restart;
3. `vmmd adoption-report --wait 90s`.

`deployctl deploy --only vmmd` restarts vmmd unguarded.

## Decision

### 1. The compute and shared lanes become eligible

`affected_units.py` stops blocking on `cross_lane` paths. A daemon of lane
`compute` or `shared` whose code changed becomes a candidate of a new
**compute** step in the edge lane. Everything that blocks the edge lane
still blocks it. A commit still waits for a full release when it also has
any of the following:

- a compute host role change (`compute_deploy`, for example
  `deploy/ansible/roles/nftables/`, a vmmd drop-in, or `node_join.yml`);
- a release unit file (`host_config.release_units`, both control-plane and
  compute unit files);
- a contract migration, `go.mod`, or any other existing blocking path.

The compute step runs only for a range that has a commit changing code
that **only** shared or compute daemons read (`compute_only_commits`). A
range that reaches them only through a package every lane reads leaves
them at base, as today. So:

- a control-plane commit keeps its under-4-minute path;
- vmmd restarts only for a change of its own, not for every edit to a
  shared package (vmmd's code changes in most merges through those).

Once the step runs, it carries every shared or compute binary whose
inputs changed since base. The per-host plan restarts only the binaries
whose code differs from what that host runs.

Each daemon rides the lane that already reaches its host:

- **schedd's control-plane service** goes in the control-plane lane's
  activation;
- **compute-node services** go in the edge lane's per-node job;
- **helpers** (the four bridges, the jail helper and the guest-disk tool)
  are replaced in the overlay and never restarted. vmmd starts each
  bridge from `/opt/faas/current/bin`, and the jail helper and the
  guest-disk tool sit beside vmmd's executable.

The lane ships switched off (`release_units.json` `compute_lane.enabled`).
While it is off, the analyzer keeps today's rules: compute binaries stay
at base, and a commit that crosses lanes blocks. It is switched on only
after build steps 2 and 3 below.

`guest/`, `images/` and kernel paths stay `pending`. They wait for the full
release, as they do today. Runtime goes last in the deploy order, so a
newer vmmd beside an older guest is the normal skew (release-compatibility.md).

### 2. Database nodes stay on their release (owner decision)

The following daemons also run on database nodes (ADR-1027) and the agent
egress host:

- vmmd;
- pgproxyd;
- dbbackupd;
- agentproxyd.

A commit that changes them ships to app compute nodes only. The database
nodes and the agent egress host keep their binaries until the next full
release. The analyzer reports these daemons as `pending` for those hosts,
the way it reports guest changes. It does not block the commit.

Database VMs have their own adoption path, so they stay out of the lane.

### 3. vmmd restarts only after the adoption proof

When the plan includes vmmd, the node's activation script wraps the restart
as follows:

1. **Check before the restart.** Run the overlay's `vmmd adoption-check
   --config /etc/faas/vmmd.toml` with the switches the restarted vmmd will
   run with. Retry while only guests are refused: 7 attempts, 10 s apart
   (ADR 20261116191802).
2. **Stand down on a refusal (owner decision).** If a guest is still
   refused, or the check names a node-level reason, the lane does not
   change the node:
   - it parks no guest;
   - it never drains;
   - the run fails with the refusal, and the commit waits for the full
     release.

   The same applies when the serving vmmd does not hand its guests off
   (`FAAS_VMMD_ADOPT_RUNNING_VMS` is off in its environment), for example a
   node not yet in `vmmd_adoption_canary_nodes`.
3. **Restart and require a clean report.** Run `deployctl deploy --only`
   with vmmd in its own wave (`soloRestart`), as today. Then run `vmmd
   adoption-report --wait 90s --already-gone <ids>`. Any refusal, a fenced
   claim, or no report in time rolls the node back and fails the run.
4. **The other daemons follow.** schedd, builderd, imaged, realtimed and
   gatewayd-internal restart in deployctl's waves after vmmd. The SLO gate
   then holds the node.

A rollback restarts the previous vmmd, which adopts the guests again.
Version-7 adoption records have been read by every release since rc.294.
The rollback is not guarded by a fresh check, because recovery comes first
(as ADR-1650 does for every unit rollback). A failed adoption in a rollback
still raises the deploy freeze.

### 4. Order and timing (owner decision)

The steps run in this order:

1. The control-plane lane, as today, including schedd on the control plane.
2. The first compute node, the canary, on the 4-minute gate, with the
   adoption report.
3. The control plane's gatewayd-public, when it changed.
4. Each remaining compute node in turn, on the 90-second follower gate.

A commit that touches compute reaches production in about 8–10 minutes
after merge. A control-plane-only commit keeps the under-4-minute path. A
failed node rolls back and stops the lane, and the deploy freeze holds
later commits.

## Consequences

- In the replay of the last 100 merges, this frees 6 of the 9 `cross_lane`
  commits (the vmmd, bridge and leaf ones). Auto-shipped commits go from
  **23 to 44** of the 63 deployable ones. 19 still wait for a full release.
- 18289035dc (nftables and a vmmd drop-in) and f21c87d48c (schedd unit
  files) still block. Shipping them needs compute host roles converged per
  node, the compute counterpart of ADR 20261018133100. That is a later
  step, not this ADR.
- An automatic deploy now restarts vmmd under tenant VMs. The safety of
  that rests on the adoption proof, which production has run on every
  no-drain full release since rc.349. The lane never drains or parks a
  guest on its own.
- Database nodes and the agent egress host can run an older vmmd,
  pgproxyd, dbbackupd or agentproxyd than the app compute nodes until the
  next full release. The base check and N±1 keep the gap to one release.

## Build order

1. **Analyzer and plan.** In `affected_units.py`, compute and shared lanes
   become eligible when nothing compute-side blocks them. The edge-lane
   plan grows a compute service set. Database-node daemons are reported as
   pending for their hosts. Tests go in `affected_units_test.py` and
   `plan_edge_rollout.py`.
2. **Adoption-guarded activation.** `unit_deploy.sh script` emits the
   steps of decision 3 when the plan includes vmmd. A test pins the order:
   check, then restart, then report, with rollback on a bad report.
3. **Metal test.** Deploy a vmmd-only unit release onto a metal host with
   live guests. Guests must be adopted, the bridges handed over and no
   guest restarted. Then force a refusal and require the lane to change
   nothing on the node.
4. **Production canary.** The first run is a manual dispatch of a
   vmmd-only commit while the owner watches. The lane becomes automatic
   after one clean run on both nodes.
   `compute_lane.enabled` stays false until then, because automatic deploys
   are on: with it true, the first compute-only merge after a full release
   would restart vmmd on production unwatched. The canary is a
   `release-units` dispatch with `deploy_environment` and
   `compute_lane: true`, which turns the lane on for that run only (in the
   run's copy of the table) and has its own build concurrency group, so
   the 10-minute schedule cannot cancel it. It needs a range with no
   compute role or unit-file change since the base, so it runs after the
   next full release, once a compute-only commit has merged.
