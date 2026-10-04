# ADR-580: Converge the control-plane bootstrap roles in CD

- **Status:** proposed
- **Date:** 2026-10-04
- **Decision:** `cd-controlplane` runs the control-plane play of
  `deploy/ansible/bootstrap.yml` before it activates a release, through the new
  `gregalectl deploy converge-control-plane` command and
  `deploy/ansible/control_plane_converge.yml`. The play runs only when a hash of
  its inputs differs from the one recorded on the host after the last successful
  convergence. The inputs are the selected plays, their roles, the shared task
  libraries (`roles/_shared`, `tasks/`, `vars/`, `group_vars/control_plane`),
  `ansible.cfg`, `requirements.yml`, the manifest-rendered inventory, and the
  operator variables. FaaS daemon restarts are deferred to release activation
  (`faas_join_defer_service_handlers`); infrastructure services restart through
  their own handlers when their configuration changed. The operator variables
  move from a workstation file into the `CP_ANSIBLE_VARS_B64` environment
  secret.
- **Why:** CD only shipped release unit files and Prometheus rules to the
  control plane. Packages, role-rendered drop-ins, Alertmanager, Caddy,
  nftables and PostgreSQL settings changed only when someone ran
  `make bootstrap-control-plane` from a workstation with a local
  `prod-vars.yml`. Merged role fixes therefore did not reach production
  (production-us ran Alertmanager in dev mode for days after the rebuild), and
  the live configuration could not be reviewed or reproduced.
- **Consequences:**
  - Role changes reach the control plane on the next platform rollout, in the
    same order as compute nodes: converge, then activate.
  - Variable-only changes converge too, because the operator variables are a
    contract input. *Amended 2026-10-04:* the compute contract now includes
    the compute operator variables (`COMPUTE_ANSIBLE_VARS_B64`) the same way;
    a rollout without a vars file keeps the previous hash.
  - The compute contract now also covers `tasks/`, `vars/` and
    `group_vars/compute_nodes`, which roles read but the hash skipped. The first
    compute rollout after this change runs full convergence once.
  - Convergence contacts only the control plane over SSH, using the verified
    fleet `COMPUTE_KNOWN_HOSTS` and `COMPUTE_SSH_KEY` that cd-compute already
    uses for the control-plane peer play. Peer private addresses come from DNS,
    as in join's skip-preflight mode.
  - The deploy job's timeout rises from 15 to 40 minutes. An unchanged contract
    costs under a minute.
  - Rollouts of releases that predate the playbook skip convergence. A
    GitHub-hosted runner cannot resolve the fleet's private names, so it skips
    convergence with a warning.
  - A missing `CP_ANSIBLE_VARS_B64` fails the rollout rather than activating a
    release whose role changes were not applied.
- **Rejected alternatives:**
  - Running the full control-plane play on every rollout: it adds minutes
    (supply-chain scanner database refresh, package checks) to every release and
    restarts infrastructure services for no reason.
  - Converging from cd-compute's `node_join_control_plane.yml`: it runs once per
    compute join in the activation lane, would repeat the work per node, and
    couples control-plane changes to compute rollouts.
  - Keeping the workstation path and documenting it: that is the status quo
    that let role fixes sit unapplied.
  - Excluding the operator variables from the hash, as compute does: a changed
    password or receiver path would never be applied.

## Rollout

1. Create `CP_ANSIBLE_VARS_B64` in each deploy environment from the variables
   the last manual control-plane bootstrap used:
   `base64 -w0 prod-vars.yml` (macOS: `base64 -i prod-vars.yml`), pasted as the
   secret value. Include the Alertmanager channel and heartbeat file paths
   (`docs/runbooks/FaasWatchdog.md`).
2. The next `cd-platform` or `cd-controlplane` run converges once (no contract
   is recorded yet) and records
   `/var/lib/faas/bootstrap/control-plane-bootstrap-contract.sha256`.
3. Later rollouts converge only when an input changes. To force a convergence,
   delete that file on the control plane.

Related: [ADR-143](143-deploy-configuration-contract.md) defined the deploy
configuration contract and its convergence checks; this ADR makes CD apply the
control-plane half of it.
