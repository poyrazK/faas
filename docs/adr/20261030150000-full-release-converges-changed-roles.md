# ADR-20261030150000: A full release converges only the control-plane roles that changed

- **Status:** proposed
- **Date:** 2026-10-30
- **Related:** ADR-580 (control-plane convergence in CD), ADR
  20261018133100 (host-configuration lane), ADR-1650 (per-unit releases)

## Context

The control-plane stage of every full release (`cd-controlplane`, "Deploy
to Control Plane Host") takes about 34 minutes on production-us. The
"Converge control-plane roles" step is most of it: 1086–1645 s on the first
attempt of every release from rc.294 to rc.308, except rc.303 and rc.304
(10–13 s).

ADR-580 runs the whole control-plane play when one hash of all its inputs
differs from the hash recorded on the host. That hash covers every
control-plane role, so almost every release changes it. The play is about
1,000 tasks at 1.2 s each, run over SSH from the runner on the control
plane to the control plane, a 2-vCPU host at a load average near 4. On
rc.306 and rc.307 the slowest tasks were:

- `grype db status`: 101 s. Nothing on the control plane runs Grype.
- the firecracker role's guest kernel build: 54 s + 35 s.
- `storage` 43 s, `host_hardening` packages 25 s.
- The other ~620 executed tasks average 1.8 s, and 379 skipped tasks
  average 0.1 s.

Most releases change a handful of roles. `scripts/ci/host_roles.py
changed`, run against the release each one replaced:

| release | roles whose hash changed | converge today |
|---|---|---|
| rc.296 | 16 | 18.1 min |
| rc.297 | 7 | 18.2 min |
| rc.299 | 4 (from rc.297) | 27.4 min |
| rc.301 | 4 | 19.1 min |
| rc.302 | 2 | 20.3 min |
| rc.305 | 8 | 24.7 min |
| rc.306 | 3 | 19.9 min |
| rc.307 | 1 (`grafana`) | 19.4 min |
| rc.308 | 4 | 25.7 min |
| rc.295, rc.300 | every role (the play's globals) | 26.9, 26.2 min |
| rc.303, rc.304 | none | 0.2 min |

The host-configuration lane already converges only changed roles:
per-role content hashes (`host_roles.py`), a record of the converged hashes
on the host (`/var/lib/faas/bootstrap/control-plane-roles.json`), a
planner (`plan_host_config.py`) and `gregalectl deploy
converge-control-plane --roles`. ADR 20261018133100 lists "the full lane
writes the record" as owed. The lane has never converged on
production-us, so the host has no record.

## Decision

The full release uses the lane's mechanism. Nothing new runs Ansible.

1. **Plan.** Before convergence, `cd-controlplane` reads the host's record
   (`unit_deploy.sh host-state`) and runs `plan_host_config.py
   --full-release <tag commit>`:
   - `roles`: the roles whose content hash differs from the record, plus
     the roles whose handlers they notify;
   - `all`: every role, when the host has no record, a record in another
     format, or other globals;
   - `contract`: no role's hash changed.

   Unlike the lane, it refuses nothing. A full release may restart
   PostgreSQL or pgbouncer, and may deploy an older commit (a rollback):
   the hashes compare content, so no ancestry check is needed.
2. **Converge.** For `roles` and `all`, the release's signed `gregalectl`
   runs with `--roles <plan> --full-release`. That passes the lane's role
   selection and environment hash, plus `faas_cp_full_release`. It passes
   neither `faas_stateful_restart_allowed: false` nor
   `faas_cp_record_contract: false`: the roles may restart PostgreSQL, as
   the whole play may, and the playbook records the ADR-580 contract. As
   in the lane, a changed operator variable or inventory since the
   record's environment converges every role. For `contract`,
   `gregalectl` runs without `--roles`, so ADR-580's contract still
   decides. That covers a change that moved no role hash, such as
   `CP_ANSIBLE_VARS_B64`.
3. **Requested roles always run.** `control_plane_converge.yml` no longer
   takes the contract fast path when roles are requested, in the lane
   too. The roles were chosen by content hash, which covers inputs outside
   `deploy/ansible` that the contract misses (`deploy/scripts/*`,
   `deploy/systemd/faas-pg-*`, the status page, `docs/DPA.md`). Before,
   the lane's "converge the roles back" step, which runs at the record's
   commit, matched the unchanged contract and converged nothing.
4. **Record.** After the play succeeds, the job writes the record (the
   release commit's hashes and the environment hash `gregalectl`
   reports) and the contract with `unit_deploy.sh host-record`, as the
   lane does after its gate. A failed play writes nothing, so the next
   run converges the same roles again. `host-record` now also writes the
   contract into the record (`contract`), for the lane too. Both planners
   treat a record whose `contract` differs from the host's contract file
   as missing, and converge every role. That happens after a convergence
   that moved the contract but not the record, such as an older tag
   redeployed through its own workflow, whose whole play rolls the roles
   back.
5. **Clean `--json`.** `gregalectl deploy converge-control-plane --json`
   sends the play's output to stderr. It used to share stdout with the
   report, so the lane's `--json > converge.json` would not have parsed.
6. **Older tags.** A tag whose `plan_host_config.py` lacks
   `--full-release` runs the whole play as before.

The record's invariant is the lane's: it is written only after a
successful convergence that ran every role whose hash differs from the
previous record (or every role), with the recorded environment. In the
`contract` case every role hash equals the record's. Either the contract
was current, which means the environment was applied when it was
recorded, or the whole play ran. In both cases the new record is true.

## Consequences

- **Time.** With the per-role times of rc.306 and rc.307 plus a ~2-minute
  floor (facts, the probe play, ~1,000 skipped tasks at 0.1 s), the
  releases in the table converge in about 3–10 minutes instead of 18–27.
  rc.307, which changed only `grafana`, would take about 3 minutes instead
  of 19. A change to the play's globals still converges every role. Over
  rc.295–rc.308 the step totals about 100 minutes instead of 246.
- **First run.** production-us has no record. The first full release
  after this change converges every role once (about 20 minutes, as
  today) and records them.
- **Drift.** Today nearly every release re-applies every role, which also
  repairs hand edits on the host. After this change a role whose content
  did not change is not re-applied. ADR-580 never promised that: an
  unchanged contract already skipped everything. To force a full
  convergence, remove `/var/lib/faas/bootstrap/control-plane-roles.json`
  on the control plane. The next full release then converges every role.
- **Roles that do housekeeping on every run** (the Grype database refresh
  in `supply_chain_scanners`, `fleet_verify`'s readiness polls) now run
  only when their content changes. On the control plane nothing reads the
  Grype database. Activation verifies the daemons itself.
- **The lane's rollback converges.** See decision 3.

## Rejected alternatives

- **A separate full-lane planner or a role-filtered playbook.** ADR
  20261018133100 rejected both: a second runner drifts from
  `bootstrap.yml`, and role tags do not reach dynamically included tasks.
- **Lane mode as is (`--roles` without `--full-release`).** It forbids
  PostgreSQL and pgbouncer restarts and records nothing. A full release
  that changes `postgres` would fail.
- **Fixing the contract to cover role inputs outside `deploy/ansible`.**
  It would still converge the whole play when any role changed.
- **Dropping the firecracker and supply-chain roles from the control-plane
  play.** That changes the control plane's topology. Per-role convergence
  already keeps them out of most releases.
