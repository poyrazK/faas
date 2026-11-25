# ADR 20261125083502 · Compute host-config lane: the unit lane converges changed compute roles

- **Status:** deferred (owner decision 2026-11-25). Measured against the
  last seven compute-role commits, neither this design nor the
  `node_join.yml` narrowing (§6) would have freed one (see "What it
  frees"). Nothing is built; revisit only with new evidence: a window
  where commits change lane roles alone, or a narrower pairing rule.
  The owner chose the four design decisions on 2026-11-25 (role converge
  in the lane, deferred handlers with an adoption-guarded restart, heavy
  roles + `node_join.yml` + unit files stay full-release-only,
  re-converge the recorded config on a failed gate).
- **Date:** 2026-11-25
- **Amends:** ADR 20261123122457 (compute unit lane, step 3 of its build
  order), ADR 20261018133100 (host-configuration lane: `compute_deploy`)
- **Related:** ADR-20261115120000 (compute converges the roles that changed;
  the role record), ADR 20261030150000 (control plane converges changed
  roles), ADR 20261122170000 (continuous unit lane), ADR 20261027151755
  (deploy path classes), `20261017-vmmd-restart-adoption.md`

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

The risks this ADR has to close: with handlers deferred, a converged
`vmmd_service` drop-in never reaches vmmd unless the lane restarts vmmd
itself (behind the adoption check); without deferral, its `try-restart
faas-vmmd` handler would bypass that check. And a rollback must leave the
node on exactly the config its role record names.

## Decision

### 1. The lane converges the changed compute roles, per node, before activation

A commit whose only compute-side flag is `compute_deploy` from **lane
roles** (§3) becomes deployable by the compute lane. In the per-node job
(canary node first, then the rest, one at a time as today):

1. **Plan.** Fetch the node's role record (`compute_role_record.sh fetch`)
   and run `host_roles.py changed --group compute_nodes --to <release sha>
   --record`. A missing or unreadable record, `all: true` (globals changed),
   or a role outside the lane roles *whose own content changed* stands the
   node down: plan `refused`, nothing touched, freeze issue as for any
   refusal. `changed` also lists roles pulled in only because a changed
   role notifies their handlers; those do not refuse on their own, and
   `affected_units_test.py` fails if a lane role notifies a handler of a
   full-release-only role (§3).
2. **Adoption check first.** When the restart set (step 4) will include
   vmmd, run the candidate's adoption check before anything converges. A
   refused guest stands the node down here, with nothing on disk changed.
3. **Converge.** Run bootstrap.yml's compute play against that node only,
   from the release commit's checkout, with
   `faas_join_converge_roles=<changed roles>`,
   `faas_join_defer_service_handlers=true` and the same manifest-rendered
   inventory and operator vars the full release uses. Neither the role
   record nor `bootstrap-contract.sha256` is written yet.
4. **Activate.** deployctl deploys the overlay as today. The restart set is
   the union of the units whose binary changed and the units the converged
   roles restart (`host_roles.py restart_roles` for those roles, mapped to
   units), so a config-only change still reaches its daemon. vmmd always
   goes through the adoption check → restart → adoption report path. A
   guest refused at this second check (the node changed since step 2)
   rolls the node back (§4) before anything restarts.
5. **Gate.** The node's adoption report and SLO gates as today.
6. **Record.** After the gate passes, write the new hashes to
   `compute-roles.json` (the same file and format node_join writes). The
   ADR-580 bootstrap contract is left alone: the next full release still
   sees it stale and converges what changed since the record, which is
   nothing the lane already converged.

The canary node's gate must pass before any other node converges.

### 2. Handlers are deferred; the lane owns every restart

The play never restarts a daemon in lane mode. The units its roles would
have restarted are added to the deployctl restart set (§1.4), so:

- `faas-vmmd` restarts only behind the adoption guard. The lane stands
  down on a guest the new vmmd would refuse, exactly as for a vmmd binary
  change.
- `faas-schedd` and `faas-gatewayd-internal` restart in deployctl's waves
  with their readiness checks.
- A task that applies state directly rather than through a handler (the
  nftables role's atomic `nft -f` reload) applies during step 3. The
  rollback (§4) reapplies the recorded ruleset the same way.

A lane-role handler that restarts any unit without honouring
`faas_join_defer_service_handlers` is a test failure, extending
`vmmd_restart_deferral_test.go` from faas-vmmd to every restart handler of
every lane role. Today that catches `vmmd_service`'s `restart
networkd-dispatcher` handler (`systemctl try-restart
networkd-dispatcher.service`, unguarded): building this lane guards it and
adds networkd-dispatcher to the role-derived restart set. `daemon-reload`
handlers are exempt (they restart nothing), and an exception list in the
test names any other handler allowed to act directly, as the nftables
reload does.

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
- **`node_join.yml`:** it drives full rollouts only, but a compute role or
  compute daemon change can depend on its step, and the lane does not run
  node_join. §6 records why narrowing this did not pay.
- **Unit files** under `compute_only_service/files/` and the other compute
  roles' `files/*.service|*.socket|*.slice`: listed in
  `host_config.release_units` for compute too, as on the control plane,
  where deployctl installs unit files from the release bundle.

Lane roles are listed explicitly too (`compute_lane.roles`), so a new
compute role is full-release-only until someone adds it:
`affected_units_test.py` fails while a compute play role is in neither
list or in both. Adding a role to the lane is an explicit review.

### 4. A failed gate re-converges the recorded config

On a failed converge, a guest refused at the activation-time adoption
check (§1.4), a failed adoption report, verify or gate, the node rolls back
in this order:

1. Re-run the same roles from the **recorded** commit's checkout: the
   commit `compute-roles.json` names, the node's last converged lane or
   full release. That is not necessarily the full release the build
   started from (§5), because an earlier lane run may have converged and
   recorded newer roles. Handlers deferred.
2. Remove the files the release added that the recorded commit's roles do
   not manage: the lane computes, per converged role, the paths the role
   templates or copies at the release commit but not at the recorded one
   (from the role's tasks, as `host_roles.py` already reads them), and
   deletes them. A lane role that retires a file must also mark it
   `state: absent`, so the forward converge removes it too. The metal step
   of the build order covers both, with a role that adds a drop-in.
3. Roll the binaries back through deployctl (vmmd adoption-guarded), and
   restart the same unit set.
4. Leave `compute-roles.json` at its previous content, which was never
   overwritten (§1.6).
5. Open the freeze issue. No other node is touched; on the canary, the
   lane stops there.

If the re-converge itself fails, the node is left as is and the freeze
issue says so. The repair is a full release (cd-compute converges every
role whose hash differs from the record, which still names the recorded
commit).

### 5. Ordering with full releases

The lane's per-node compute job already serialises with cd-compute through
the preflight's `base` check (it stands down while prod runs a full release
other than the build's base: the newest full-release tag). In this ADR
"base" means only that tag; the rollback source is the recorded commit
(§4). The converge step adds nothing new to race:
it runs inside that job, on one node at a time.

### 6. `node_join.yml` alone holding no lane: rejected

Considered and built on the PR (owner chose it 2026-11-25), then dropped
before merge. The `compute_only` playbooks (`node_join.yml`,
`node_join_accept_release.yml`, `node_join_contract_probe.yml`) run only
inside `gregalectl deploy join`, so a change to one alone affects only the
next full rollout. But a vmmd or gateway change can depend on the playbook
step, in the same commit or any later one, and the compute and edge lanes
ship the whole range since the last full release. The safe rule therefore
holds the playbook whenever the range also changes a compute role path, an
input of a binary a lane runs on compute hosts (not `go.mod`/`go.sum`, not
held database-node binaries) or the build recipe. Three code reviews
converged on that rule.

Under it, nothing is freed in practice: those binaries import most of the
shared packages (`pkg/api`, `pkg/manifest`, the vmmd and schedd protos;
409 package directories). In 14eb61e7fb's window (rc.375 → rc.376), 41
such non-test Go files changed before it and 92 after it.
`node_join.yml` stays `compute_deploy`.

## What it frees

The seven commits:

| commit | blocked by, under §1–§4 and §6 |
| --- | --- |
| 14eb61e7fb | `node_join.yml`, paired range-wide with compute-side code (§6) |
| de9bcafc47 | `node_join.yml` paired with `pkg/manifest` in the same commit |
| 18289035dc | `deploy/manifest/production/ovh-live.template.yaml` (deploy, blocking); its roles (`nftables`, `vmmd_service` drop-in) would be lane roles |
| 0f872e67ae | `group_vars/compute_nodes` (global) |
| 361b9afe87 | `group_vars/compute_nodes`; `node_join.yml` |
| f21c87d48c | unit file; control-plane `release_units` |
| 902041da04 | `node_join.yml` with `compute_admission`/`compute_only_service` |

0 of 7. The one commit whose roles the executor could converge also
changed the production manifest. Its value depends on how often a commit
changes only lane roles, which this window never did. Per-variable
`group_vars` ownership (the follow-up ADR-20261115120000 names) would add
0f872e67ae only once the executor exists.

## Open before building

Reviews of this ADR against the code it relies on (2026-11-25) found gaps a
revival must close first; the decisions above assume them closed:

1. **The role record names no commit.** `compute-roles.json` is
   `host_roles.py` `group_hashes` output plus `.environment`. §4.1's
   "recorded commit" needs a `commit` field that node_join and the lane
   both write.
2. **The environment hash.** node_join converges every role when the
   record's `.environment` (manifest-rendered inventory and operator vars)
   differs; `host_roles.py changed --record` never compares it. The lane
   must refuse (or treat as `all: true`) a node whose environment changed,
   and record only the environment it converged under.
3. **Unguarded restart handlers.** Besides `vmmd_service`'s
   networkd-dispatcher, these compute roles' handlers restart or reload
   with no `faas_join_defer_service_handlers` guard: `tenant_egress_client`
   and `agent_egress_client` (`wg-quick@`, guest egress), `overlay`
   (`faas-overlay`, nftables), `node_exporter`, `alloy`, `promtail`, and
   part of `gatewayd_public_edge_service`. Each is guarded, excepted by
   name, or kept full-release-only before it becomes a lane role.
4. **Restarting the derived set.** `host_roles.restart_roles` is a function
   that needs a services list, not a CLI; deployctl's waves only know faas
   units. Each unit the set can contain (faas-imaged,
   faas-gatewayd-public-edge, faas-cloudflared, networkd-dispatcher,
   `wg-quick@*`, faas-overlay, …) needs a stated restart path: a deployctl
   wave or a direct, guarded restart.
5. **Files a release added.** `host_roles.py` hashes role sources; it does
   not parse `dest:` paths. §4.2 needs a new destination extractor for
   template, copy and file tasks.
6. **Changed versus pulled-in roles.** `host_roles.changed()` returns one
   merged list. §1.1's rule (handler-only roles do not refuse) needs a split
   output, or the plan compares record and release hashes itself.
7. **Rollback adoption refusal.** If the old vmmd's adoption check refuses
   a guest woken under the new vmmd during the gate, the rollback stands
   down with the node on the recorded config and the new binary, record
   unchanged, freeze issue naming the refused guest; the repair is a full
   release.

## Build order (if revived)

1. `affected_units.py`: `compute_deploy` splits into lane-role changes
   (deployable by the compute lane) and full-release-only changes (still
   `compute_deploy`); `full_release_roles` and compute `release_units` in
   `release_units.json`; tests over the seven commits above.
2. Per-node converge + role-derived restart set + record write in the
   compute job; deferral test extended to every lane-restarted unit.
3. Rollback: re-converge the recorded commit, remove release-added files, then the binary rollback.
4. Metal run on a native KVM host: converge a `vmmd_service` drop-in change
   with a live guest; prove adoption, then a forced gate failure that
   re-converges the recorded commit and removes the added drop-in.
5. Prod canary on the next lane-role commit, owner watching.

## Consequences

- A compute role change ships in the 8–10 min a compute commit already
  takes, plus about 1–3 min of converge per node.
- The lane now writes the compute role record, so a later full release
  converges less (only what changed since the lane's record).
- A node can run host config newer than its full-release contract between
  full releases. The record says which; `release_integrity` and the
  convergence report keep judging binaries and the contract as before.
