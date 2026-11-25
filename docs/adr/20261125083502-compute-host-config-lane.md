# ADR 20261125083502 · Compute host-config lane: the unit lane converges changed compute roles

- **Status:** proposed. The owner chose the four design decisions on
  2026-11-25 (role converge in the lane, deferred handlers with an
  adoption-guarded restart, heavy roles + `node_join.yml` + unit files stay
  full-release-only, re-converge the base on a failed gate).
- **Date:** 2026-11-25
- **Amends:** ADR 20261123122457 (compute unit lane, step 3 of its build
  order), ADR 20261018133100 (host-configuration lane: `compute_deploy`)
- **Related:** ADR-20261115120000 (compute converges the roles that changed;
  the role record), ADR 20261030150000 (control plane converges changed
  roles), ADR 20261122170000 (continuous unit lane), ADR 20261027151755
  (deploy path classes), ADR 20261017 (vmmd restart adoption)

## Context

The compute lane is live since 2026-11-24 (canary 3, run 45502098778:
bhs-2 adopted 4/4, bhs-3 11/11, every SLO gate passed). It ships compute
binaries. A commit that changes a path a compute host role reads is still
`compute_deploy`: the edge and compute lanes stop at it until the next full
release, and every later commit waits behind it (whole-commit prefix).

`compute_deploy` commits per full-release window, rc.370 → main
(2026-11-22 to 2026-11-24, `affected_units.classify_deploy` per
first-parent commit):

| commit | what it touched on compute |
| --- | --- |
| f21c87d48c | `compute_only_service/files/faas-schedd.service` (unit file) |
| 361b9afe87 | `group_vars/compute_nodes/release_commands.yml`, `node_join.yml` |
| 18289035dc | `nftables` defaults, tasks, policy template; `vmmd_service` `99-faas-egress.conf.j2` |
| 14eb61e7fb | `node_join.yml` |
| 0f872e67ae | `group_vars/compute_nodes/internal_egress_destinations.yml`, `vmmd_service` defaults |
| de9bcafc47 | `node_join.yml` |
| 902041da04 | `node_join.yml`, `compute_admission` tasks, `compute_only_service` defaults |

They fall in 5 of the last 13 windows. The pieces this lane needs already
exist:

- `host_roles.py hashes|changed --group compute_nodes` hashes the 41 roles of
  bootstrap.yml's compute play and computes the changed set with the roles
  whose handlers they notify (ADR-20261115120000).
- Every compute role entry is selectable with `faas_join_converge_roles`;
  a node records its converged hashes in
  `/var/lib/faas/bootstrap/compute-roles.json`
  (`scripts/ci/compute_role_record.sh` reads it).
- Every handler that restarts `faas-vmmd` honours
  `faas_join_defer_service_handlers` (`pkg/daemonunitspec`
  `vmmd_restart_deferral_test.go` enforces it); `gregalectl deploy join` and
  `converge-control-plane` already set it.
- The per-node compute job restarts vmmd behind the adoption check and
  report, stands down on a refused guest and rolls back on a failed gate
  (ADR 20261123122457).

The risk this ADR has to close: a converged `vmmd_service` drop-in notifies
`systemctl try-restart faas-vmmd`, which bypasses the adoption check.

## Decision

### 1. The lane converges the changed compute roles, per node, before activation

A commit whose only compute-side flag is `compute_deploy` from **lane
roles** (§3) becomes deployable by the compute lane. In the per-node job
(canary node first, then the rest, one at a time as today):

1. **Plan.** Fetch the node's role record (`compute_role_record.sh fetch`)
   and run `host_roles.py changed --group compute_nodes --to <release sha>
   --record`. A missing or unreadable record, `all: true` (globals changed),
   or a changed role outside the lane roles stands the node down: plan
   `refused`, nothing touched, freeze issue as for any refusal.
2. **Converge.** Run bootstrap.yml's compute play against that node only,
   from the release commit's checkout, with
   `faas_join_converge_roles=<changed roles>`,
   `faas_join_defer_service_handlers=true` and the same manifest-rendered
   inventory and operator vars the full release uses. Neither the role
   record nor `bootstrap-contract.sha256` is written yet.
3. **Activate.** deployctl deploys the overlay as today. The restart set is
   the union of the units whose binary changed and the units the converged
   roles restart (`host_roles.py restart_roles` for those roles, mapped to
   units), so a config-only change still reaches its daemon. vmmd always
   goes through the adoption check → restart → adoption report path; a
   refused guest stands the node down before anything restarts (step 4).
4. **Gate.** The node's adoption report and SLO gates as today.
5. **Record.** After the gate passes, write the new hashes to
   `compute-roles.json` (the same file and format node_join writes). The
   ADR-580 bootstrap contract is left alone: the next full release still
   sees it stale and converges what changed since the record, which is
   nothing the lane already converged.

The canary node's gate must pass before any other node converges.

### 2. Handlers are deferred; the lane owns every restart

The play never restarts a daemon in lane mode. The units its roles would
have restarted are added to the deployctl restart set (§1.3), so:

- `faas-vmmd` restarts only behind the adoption guard. The lane stands
  down on a guest the new vmmd would refuse, exactly as for a vmmd binary
  change.
- `faas-schedd` and `faas-gatewayd-internal` restart in deployctl's waves
  with their readiness checks.
- A task that applies state directly rather than through a handler (the
  nftables role's atomic `nft -f` reload) applies during step 2. The
  rollback (§4) reapplies the base ruleset the same way.

A lane-role handler that restarts a unit without honouring
`faas_join_defer_service_handlers` is a test failure, extending
`vmmd_restart_deferral_test.go` from faas-vmmd to every unit the lane
restarts.

### 3. What stays full-release-only

These stay in `release_units.json` (`compute_lane.full_release_roles`,
next to `held`):

- **Heavy or host-level roles:** `storage`, `lvm`, `xfs`, `grub`, `zswap`,
  `firecracker` (kernel and binaries), `host_hardening`,
  `supply_chain_scanners`, `cgroups_v2`, `systemd_slices`, `tang_server`,
  `tetragon`, `gcp_ops_agent`. They are slow, touch the boot path or the
  host's own security posture, and rarely change.
- **Globals:** `group_vars/compute_nodes/*`, the compute play bodies,
  `ansible.cfg` and the collection pins. `host_roles.py changed` already
  reports these as `all: true`, so the lane refuses them (§1.1).
- **`node_join.yml`:** keeps its `compute_only` class and blocks. It
  drives full rollouts only, but a role change can depend on a node_join
  step made in the same commit, and the lane does not run node_join.
- **Unit files** under `compute_only_service/files/` and the other compute
  roles' `files/*.service|*.socket|*.slice`: listed in
  `host_config.release_units` for compute too, as on the control plane,
  where deployctl installs unit files from the release bundle.

Lane roles are listed explicitly too (`compute_lane.roles`), so a new
compute role is full-release-only until someone adds it:
`affected_units_test.py` fails while a compute play role is in neither
list or in both. Adding a role to the lane is an explicit review.

### 4. A failed gate re-converges the base

On a failed gate (or a failed converge, adoption report or verify), the
node rolls back in this order:

1. Re-run the same roles from the **base** commit's checkout (the node's
   current unit release), handlers deferred.
2. Roll the binaries back through deployctl (vmmd adoption-guarded), and
   restart the same unit set.
3. Leave `compute-roles.json` at its previous content, which was never
   overwritten (§1.5).
4. Open the freeze issue. No other node is touched; on the canary, the
   lane stops there.

If the base re-converge itself fails, the node is left as is and the freeze
issue says so. The repair is a full release (cd-compute converges every
role whose hash differs from the record, which still names the base).

### 5. Ordering with full releases

The lane's per-node compute job already serialises with cd-compute through
the preflight's `base` check (it stands down while prod runs a full release
other than the build's base). The converge step adds nothing new to race:
it runs inside that job, on one node at a time.

## What it frees

The same seven commits, under §3:

| commit | blocked by | lane? |
| --- | --- | --- |
| 18289035dc | — (`nftables`, `vmmd_service` drop-in) | **yes** |
| 0f872e67ae | `group_vars/compute_nodes` (global) | no |
| 361b9afe87 | `group_vars/compute_nodes`, `node_join.yml` | no |
| f21c87d48c | unit file | no |
| 14eb61e7fb | `node_join.yml` | no |
| de9bcafc47 | `node_join.yml` | no |
| 902041da04 | `node_join.yml` | no |

1 of 7. `node_join.yml` alone holds 4 of them and `group_vars/compute_nodes`
2, so they decide whether this lane pays off. Two narrowings are left open
for the owner, each a separate amendment:

- **`node_join.yml` → inert for the lanes** when the same commit changes no
  lane role (14eb61e7fb, de9bcafc47): the change only affects the next full
  rollout, which runs it. Frees 2 more.
- **Per-variable `group_vars` ownership:** treat a
  `group_vars/compute_nodes/<file>.yml` change as a change to the roles that
  read its variables instead of a global (the follow-up ADR-20261115120000
  already names). Frees 0f872e67ae.

## Build order

1. `affected_units.py`: `compute_deploy` splits into lane-role changes
   (deployable by the compute lane) and full-release-only changes (still
   `compute_deploy`); `full_release_roles` and compute `release_units` in
   `release_units.json`; tests over the seven commits above.
2. Per-node converge + role-derived restart set + record write in the
   compute job; deferral test extended to every lane-restarted unit.
3. Rollback: base re-converge before the binary rollback.
4. Metal run on a native KVM host: converge a `vmmd_service` drop-in change
   with a live guest; prove adoption, then a forced gate failure that
   re-converges the base.
5. Prod canary on the next lane-role commit, owner watching.

## Consequences

- A compute role change ships in the 8–10 min a compute commit already
  takes, plus about 1–3 min of converge per node.
- The lane now writes the compute role record, so a later full release
  converges less (only what changed since the lane's record).
- A node can run host config newer than its full-release contract between
  full releases. The record says which; `release_integrity` and the
  convergence report keep judging binaries and the contract as before.
