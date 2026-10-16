# ADR-1360 · Parallel-safe compute prepare when the role contract changed

- **Status:** proposed
- **Date:** 2026-10-16
- **Decision:** the phased compute rollout's prepare step runs on every
  existing managed compute host in parallel even when the host's recorded
  bootstrap contract is stale. Prepare never converges OS or roles and never
  records the contract: `bootstrap.yml` ends every host when
  `faas_join_rollout_phase == 'prepare'`, and `node_join.yml` skips the
  contract write in prepare. Prepare stages and verifies the release, runs
  the runtime-base pre-stage, and refreshes the Grype database
  (`supply_chain_scanners/tasks/grype_db.yml`). Activation stays serialized.
  `gregalectl deploy join-node --activate-prepared` probes the node's
  recorded contract (`node_join_contract_probe.yml`, read-only). If it is
  current, activation starts at "Verify the prepared rollout marker" as
  before. If it is stale, activation runs `node_join.yml` from the top:
  roles converge before the drain, the contract is recorded, and the
  pre-stage section is skipped because the prepared marker proves it ran.
  Fresh hosts still take the full serialized join.
- **Why:**
  - The prepare refusal ("requires a current managed-host bootstrap
    contract") made phased mode unusable whenever `node_join.yml`, a shared
    root or any of the 29 compute roles changed. That was 8 of the 13
    release-to-release steps between rc.248 and rc.270, so every production
    rollout ran `full` mode and prepared nodes one at a time.
  - On rc.269, fsn-3's 22-minute node run spent 4.2 min in the pre-stage,
    3.9 min refreshing the Grype database and about 2 min staging and
    verifying the release. None of that touches serving daemons, but `full`
    mode serialized it per node. Moving it into the parallel prepare saves
    about 8–10 min per node, so the saving grows with the fleet.
  - The refusal existed to keep role convergence, which can restart daemons,
    from running on several serving nodes at once. This decision keeps that
    guarantee: roles still converge one node at a time, inside activation.
- **Consequences:**
  - A stale-contract activation re-runs the release staging tasks of the
    first play. They are idempotent (checksummed copies, a verified
    re-download), so they cost little next to role convergence.
  - The pre-stage runs the candidate before that release's role changes are
    applied. If the candidate needs a resource only a changed role creates,
    the pre-stage fails during prepare. No node has been drained at that
    point; the operator re-dispatches with `compute_rollout_mode=full`.
  - If a role change alters a base reference, the bases staged during
    prepare are the old ones. The serving imaged stages the new ones before
    READY after the switch: correct, just slower for that release.
  - Two prepare jobs now run at once on the control plane's runners
    (`gregale-cp`, `gregale-cp-2`), adding a few minutes of SSH and transfer
    load to the 2-vCPU control plane during a rollout.
  - The rollout report records `roles_converged` when an activation
    converged roles.
- **Rejected alternatives:**
  - Converging roles during prepare: roles can restart daemons, which on
    every node at once is a fleet-wide outage.
  - Always activating from the top: it re-runs the first play even when the
    contract is current, slowing the common fast path. The probe keeps it.
  - Per-role incremental convergence: a larger saving, but every input a
    role reads from outside its directory would need to be in its hash.
    Left as a possible follow-up.
- **Builds on:** the bootstrap contract hash (`joinBootstrapContractHash`)
  and the prepare/activate split of `gregalectl deploy join-node`
  (`--prepare-only`, `--activate-prepared`).
