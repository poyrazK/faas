# ADR-20261115120000: Full releases converge only what changed, on the control plane and on compute

- **Status:** proposed
- **Date:** 2026-11-15
- **Related:** ADR-580 (control-plane convergence in CD), ADR-20261030150000
  (a full release converges only the control-plane roles that changed),
  ADR-20261018133100 (host-configuration lane), ADR-1650 (per-unit
  releases), ADR-20261025095348 (parallel compute preparation)

## Context

A unit release (ADR-1650) ships in minutes but refuses most ranges: on
2026-11-15 the 44 pull requests merged since rc.344 had compute roles,
guest init, images, a PostgreSQL role and a dependency bump in them, so
`release-units.yml` reported nothing deployable. Those changes need a full
release, and a full release costs about 95 minutes of rollout when nothing
fails (cd-platform, rc.344, attempt 5):

| stage | time | where it goes |
|---|---|---|
| control plane | 27 min | 22 min "Converge control-plane roles" (784 tasks); activation 99 s |
| compute prepare (parallel) | 12 min | release staging |
| compute roll, fsn-2 then fsn-3 | 25 min each | 18 min "adopt the compute host" (748 tasks, full bootstrap); 6 min ingress proof |
| convergence report | 5–11 min | |

Both convergences run every role of their play. Neither should.

### The control plane: a hashing bug undid ADR-20261030150000

ADR-20261030150000 already converges only the control-plane roles whose
content hash changed. On rc.344 it did not:

- Attempt 1 (runner `gregale-cp`) planned 14 of 53 roles, then the play
  logged "Control-plane bootstrap contract changed; converging the
  control-plane roles." with no list: `control_plane_converge.yml` drops the
  selection when the host's recorded environment hash differs, and it did.
- Attempt 2 (runner `gregale-cp-2`) planned no role at all ("no role's
  content hash changed", attempt 1 had recorded them), yet the ADR-580
  contract differed and the whole play ran again: 22 minutes.

Both hashes include the operator vars file, and CD appends to it the
absolute paths of the seal-domain keys it stages next to it
(`faas_seal_domain_<domain>_identity_src: "$RUNNER_TEMP/cp-converge/seal-domains/<domain>.age"`,
`scripts/ci/seal_domain_vars.sh`). The control-plane host runs two runners
(`/opt/actions-runner`, `/opt/actions-runner-2`), and the
host-configuration lane stages into `host-config/` instead of
`cp-converge/`. Any deploy that lands on another runner or lane than the
last one sees a moved environment and contract. The history agrees: rc.342
followed a deploy on the same runner and found the contract current (4
tasks); rc.343 and rc.344 changed runners and ran the whole play.

### Compute: one contract for every role

A compute node records one hash of every input of the compute play
(`/var/lib/faas/bootstrap/bootstrap-contract.sha256`, `gregalectl deploy
join`). Any compute role change makes it stale, and node_join then runs the
full bootstrap: OS, storage, hardening, every daemon role, about 750 tasks
and 18 minutes per node, serially. `scripts/ci/host_roles.py` already hashes
the `compute_nodes` group per role and computes the changed set with the
roles whose handlers they notify; nothing uses it for compute.

## Decision

### 1. The vars file's directory is not a convergence input

`gregalectl deploy converge-control-plane` hashes the operator vars file
with its own directory replaced by a fixed placeholder, in both the ADR-580
contract and the environment hash. The staged files' content still reaches
the hashes: `seal_domain_vars.sh` writes every domain's recipient into the
vars file, so a rotated key changes it. A path outside the vars directory
is content and stays as written.

The first deploy after this change sees a new contract and environment once
and converges every role; every later one converges the roles that changed.

### 2. Compute converges the roles that changed

The control plane's design, applied to the compute play:

- Every role entry of the compute play in `bootstrap.yml` carries the
  selection condition `faas_join_converge_roles | default(none) is none or
  '<role>' in faas_join_converge_roles`. `host_roles.py` drops it before
  hashing, as it does `faas_cp_converge_roles`.
- After a successful convergence node_join writes the role record,
  `/var/lib/faas/bootstrap/compute-roles.json` (`host_roles.py hashes
  --group compute_nodes` at the release commit), next to the contract, in
  the same play and under the same condition (never in `prepare`).
- cd-compute plans before it joins: it reads the node's record over SSH and
  runs `host_roles.py changed --group compute_nodes --record`. The result is
  passed to `gregalectl deploy join --roles` when it names some roles and
  not all, with the record to write as `--role-record-file`.
  `gregalectl` refuses a role the compute play does not run.
- Everything else stays: no record, another format or changed globals
  (play bodies, `ansible.cfg`, collection pins, `group_vars`, shared task
  libraries) converge every role, as today; a current contract still skips
  role convergence entirely; preparation still never converges roles.

### Not in scope

- The compute roll order (fsn-2, then fsn-3). A canary then parallel
  activation would save another ~25 minutes on two nodes; it is a separate
  decision about blast radius.
- The convergence report that failed all five rc.344 attempts.
- Widening unit releases to compute daemons, guest init or images.

### What the planner picks on recent releases

`host_roles.py changed --group compute_nodes`, with the earlier release's
hashes as the node's record:

| range | compute roles converged |
|---|---|
| rc.343 → rc.344 | 11 of 40: nftables, serving_probe, seal_domains, vmmd_service, geoip, gatewayd_internal_service, compute_only_service, alloy, promtail, role_convergence, fleet_verify |
| rc.344 → main (2026-11-15) | all 40: #7338 added `group_vars/compute_nodes/vmmd_restore_speedups.yml`, and group_vars are globals |

The slow roles of a full bootstrap (storage, firecracker's kernel build,
host_hardening's packages, supply_chain_scanners' Grype database) changed
in neither range. A group_vars change still converges every role; narrowing
that to the roles that read the variable is a possible follow-up.

## Consequences

- A typical full release converges a handful of control-plane roles instead
  of 53 (ADR-20261030150000 measured 2–16 changed roles per release, about
  1–5 minutes) and a handful of compute roles per node instead of the full
  bootstrap. Expected rollout: about 95 minutes down to about 45, with the
  release build (20–40 minutes) unchanged.
- A retry after a failure no longer pays for a second whole-play converge.
- A compute change that a role's hash does not see would be skipped. The
  hash covers a role's tasks, handlers, defaults, vars, meta, every file
  and template it names, its play entry, and the group's globals; a path a
  role reads that `host_roles.py check` cannot resolve fails CI already.
- Hardening roles (`host_hardening`, `nftables`, `release_integrity`,
  `tetragon`) are no longer re-applied on every release, only when their
  content or the globals change. Drift that happens on the host between
  releases is no longer reset by the next release; `role_convergence` and
  `fleet_verify` run when they change like any other role.
