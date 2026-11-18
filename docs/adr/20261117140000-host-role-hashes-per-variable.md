# ADR 20261117140000 · A host role's hash covers only the group variables it reads

- **Status:** proposed (2026-11-17)
- **Date:** 2026-11-17
- **Amends:** [ADR-20261018133100](20261018133100-host-configuration-lane.md)
  (what a role's content is) and
  [ADR-20261115120000](20261115120000-full-release-converge-speed.md)
  (changed-roles-only converge)
- **Related:** ADR-580 (control-plane converge contract)

## Context

ADR-20261115120000 converges only the roles whose content hash changed. Its
globals, which change every role, include every file under `group_vars/` and
`host_vars/`.

Almost every release changes one of those files, because operator switches
live there (`perf_switches.yml`, `artifact_signatures.yml`):

- `host_roles.py changed` answered "every role" for both compute
  releases on 2026-11-16 and 2026-11-17 (rc.347→rc.348, rc.348→rc.349).
- Both nodes ran all 40 compute roles, about 1,200 Ansible tasks each.
  That was 13–14 minutes of node activation on fsn-3 (rc.349: 806 s).
- No role reads the switches those releases added
  (`artifact_signature_require_v2`, `builderd_buildkit_seed`,
  `builderd_defer_cache_export`). Only `node_join.yml` reads them, and it
  renders their drop-ins on every rollout.

## Decision

`scripts/ci/host_roles.py` hashes group and host variables per variable.
The record format becomes version 2, so the first release with this change
converges every role once.

1. **What is a variable.** Each `group_vars/<group>`, `group_vars/all` and
   `host_vars` file is split into its top-level keys, with comments dropped.
   A variable's definition is the text of its key, from every file that
   defines it.
2. **What a role reads.** A role reads every variable whose name appears in
   its play entry or in any of its inputs, and every variable those
   definitions name in turn. Its inputs are its files, including templates,
   the files they reference, and included roles. A role's hash includes those
   definitions, so changing a variable converges exactly the roles that read
   it.
3. **Globals.**
   - `ansible.cfg` and `requirements.yml` stay globals.
   - So do the plays' bodies, and every variable those bodies name.
   - A vars file that is not a plain top-level mapping is a global file.
4. **Computed names.** A role that reads variables by a computed name
   (`vars[...]`, `lookup('vars', ...)`, `hostvars`, `varnames`) reads every
   variable.
5. **Unchanged.** `group_owners` still maps vars files to every role (`*`),
   so the unit-release lane's path classification does not change.

Replaying recent releases with the new hashes:

| Release | Compute roles | Control-plane roles |
|---|---|---|
| rc.345→rc.346 | 6 | 2 |
| rc.346→rc.347 | 3 | 1 |
| rc.347→rc.348 | 7 | 2 |
| rc.348→rc.349 | 4 | 9 |

Before this change, each of these converged every role whenever group_vars
changed (40 on compute).

## Consequences

- A release that only flips switches the converge playbooks read converges
  no role for them.
- **Residual risk:** a role that reads a group variable without naming it
  in any input, and without a computed-name read. One example is a variable
  passed in from a play's `vars:` under another name; the play body is a
  global, so a rename there still converges everything. Such a role misses a
  change until its own content or the globals change. The full play still
  runs on a fresh host, a record mismatch, or a stale bootstrap contract.
- An operator can force a full converge exactly as before. A node without a
  record, or with a record in another format, converges everything.
