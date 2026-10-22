# ADR 20261018133100 · Host-configuration lane

- **Status:** proposed
- **Date:** 2026-10-18
- **Related:** ADR-580 (control-plane convergence in CD), ADR-1650 (per-unit
  releases), ADR-1736 (zero-downtime restarts and the two-generation
  hand-off), ADR 20261018084150 (expand-only migrations in per-unit
  releases), ADR 202610171900 (release compatibility gates), ADR-143
  (deploy configuration contract)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-1650's unit lanes ship changed daemons over the full release the hosts
run, and ADR 20261018084150 lets expand-only migrations ride them. Every
change under `deploy/` still closed both lanes until the next full release
(55-75 minutes, with drains). Full releases converge the control-plane roles
before activation (ADR-580), and that step alone takes about 30 minutes on
production-us when its contract changed. ADR-1650 left the fix as an open
item: run host configuration in its own lane, keyed by a per-role content
hash, instead of inside every activation.

**What closed the lane.** For each of the last 15 release tags we walked
main's first-parent history from the tag and found the first merge that
closed the control-plane lane, with migrations taken as lane-eligible. That
merge was a `deploy/` change after 14 of the 15 tags. The fifteenth was a
go.mod change.

| first closer | tags | `deploy/` paths it changed | what reads them on the control plane |
|---|---|---|---|
| e98d72e2 gateway service chains | 5 | `deploy/systemd/faas-gatewayd-internal.service`, compute role `gatewayd_internal_service` | nothing: `deploy/systemd/` is `deployctl generate`'s legacy and dev-VM tree |
| 98ec0693 compute app edge role | 2 | `bootstrap.yml` (compute play only), `gatewayd_public_service` tasks and templates, `nftables`, compute roles | two control-plane roles |
| e1bfe336 storage cache | 2 | `roles/_shared/files/storage.env.example` | `control_plane_service`, which installs it |
| 60fe3e1c, 2dcaf44c pool budgets | 1 + 1 | `postgres_capacity/defaults/main.yml` | `postgres_capacity` |
| 8b66d31d pool alert | 1 | `host_hardening` task and template, `prometheus/files/faas.rules.yml` | `host_hardening`, `prometheus` |
| 78cc79ad ADR renumber | 1 | `postgres` files and tasks, `prometheus/files/pg_backup.rules.yml` | `postgres`, `prometheus` |
| 25bfa52e observability | 1 | `grafana` files and tasks | `grafana` |

By category:

- Role content of control-plane roles (tasks, defaults, templates, files):
  9 tags.
- A file nothing on the control plane reads: 5 tags.
- Unit-file-only changes: none. The one unit file among the closers was a
  compute unit. Doing unit files first would have kept the lane open after
  none of the 15 tags.
- The control-plane daemon unit files that the release bundle installs
  changed in none of the closers.

So the common case is a role change that converging one role would apply.
The cheapest part, not blocking on files no host role reads, comes free with
the same path classification. Three of the nine role closers (the two
`postgres_capacity` budgets and the `postgres` change) are roles that can
restart PostgreSQL; those keep closing the lane (below).

## Decision

### What a role's content hash covers

`scripts/ci/host_roles.py` computes one sha256 per role of a bootstrap play,
for a host group (`control_plane`, `compute_nodes`). A role's content is
everything the repository decides about what Ansible does when it runs the
role:

- the role's own files: tasks, handlers, defaults, vars, meta, templates
  and files, except documentation (`*.md`) and tests;
- every repository path its task, handler, variable and meta files name
  outside the role, resolved the way Ansible resolves them:
  - relative `src:` and `include_tasks:` paths;
  - `{{ role_path }}/...` paths;
  - `{{ playbook_dir }}/...` paths.

  This brings in the shared task library and templates, the `vars/` files,
  `deploy/systemd/faas-pg-*` (postgres_backup), `deploy/scripts/*`,
  `deploy/statuspage/index.html` and `docs/DPA.md`. A directory counts as
  every file under it. A test fails when a reference does not resolve, so
  no input escapes its role's hash;
- the content of the roles it includes (include_role, import_role, meta
  dependencies);
- its entry in the play (the `- role:` item with its `when:`, `tags:` and
  vars), without comments and without the selection condition below.

A group's globals are what every role of the group shares:

- the bodies of the `bootstrap.yml` plays that target the group, without
  their `roles:` lists;
- `ansible.cfg`;
- `requirements.yml` (collection pins);
- the group's `group_vars` and the `host_vars`.

A globals change changes every role. The converge playbooks themselves
(`control_plane_converge.yml`, `node_join.yml`) are lane machinery, not role
content.

Rendered systemd unit files and nftables rules are role content: the roles
render them from their templates and files, so the hash covers their
sources. Anything rendered from inventory or operator variables is covered
by the environment hash below. Hashes depend on content only, so a revert
within a range changes nothing.

### How the lane decides which roles changed

- **In CI, per range.** `affected_units.py` hashes both groups at the base
  release and at the head. It sorts every changed `deploy/` path by what
  reads it (`host_roles.group_owners` at base or head):
  - `host_config`: a control-plane role reads the path. This is not
    blocking. The same goes for a control-plane bundle file a role also
    installs, such as `docs/DPA.md` or
    `pkg/promqlrules/data_placement.yaml`: unit activations never install
    those, and the full job's copy matches the role's. The report names the roles whose hash changed and
    `host-<newest commit that changed one>`. The control-plane lane
    carries it, so a range that changes only roles is deployable.
  - `stateful_restart`: the range changed a control-plane role that can
    restart or stop a stateful service, or every role's globals. This is
    blocking for both lanes. See "Stateful services" below.
  - `compute_deploy`: a compute role reads the path. It is pending for the
    control-plane lane and holds the edge lane, as `compute_only_paths`
    did. The split now comes from the plays rather than a hand-kept list.
  - `release_units`: a unit file or tmpfiles rule that cd-controlplane's
    bundle installs through deployctl at every activation. This stays
    blocking (below).
  - `deploy`: any other `deploy/` path a release may read, such as
    manifests, other playbooks and drill scripts. This stays blocking.
  - No flag: documentation, tests, and trees no release installs
    (`host_config.outside_releases`). These are `deploy/systemd/` except
    the files a role names, the `deploy/grafana/` mirror of the grafana
    role's dashboards, and `deploy/cloudflare/` workers, which have their
    own workflows. A test checks that no release workflow reads those trees.
- **At deploy time, per host.** `scripts/ci/plan_host_config.py` compares
  the release commit's hashes with the host's record:
  - The roles whose hash differs are converged, plus every role whose
    handler they notify. A role's `when:` also gates its handlers; we
    checked this with ansible-core 2.21.2. So a role converged alone must
    bring the role that owns a handler it notifies. Today that is
    `s3_gateway_service` → `gatewayd_public_service`.
  - With no record, a record in another format, or other globals, every
    role converges.
  - A record from a commit that is not an ancestor of the release commit
    is refused, so host configuration never goes backwards.
  - A role that can restart a stateful service, or the group's globals,
    differing from the host's full release is refused. Otherwise such a
    role is never selected on its own, since its content is the full
    release's. It is selected only when every role converges, and then
    under the restart guard below.

### Where the converged hash is recorded

`/var/lib/faas/bootstrap/control-plane-roles.json` (root, 0600, beside
ADR-580's contract file) holds `{format_version, kind:
"gregale-host-config", group, commit, globals, order, roles: {role:
hash}, environment}`. `environment` is the hash of the rendered inventory
and the operator variables (`CP_ANSIBLE_VARS_B64`), which `gregalectl`
reports. The lane writes the record and the ADR-580 contract together,
only after its SLO gate passes. A failed run therefore leaves the record
describing the content it rolls back to. A full release does not write the
record yet (follow-up). The lane's next diff then starts from the older
record and re-converges roles the full release already applied. That is
harmless, because convergence is idempotent.

### How it applies: the ADR-580 path, with a role filter

There is no new Ansible runner. `gregalectl deploy converge-control-plane`
gains `--roles a,b`. It uses the same rendered inventory, DNS-seeded peers,
verified host key and contract, and it passes:

- `faas_cp_converge_roles_requested`;
- the environment hash;
- `faas_cp_record_contract: false`.

Every control-plane role entry in `bootstrap.yml` carries
`faas_cp_converge_roles | default(none) is none or '<role>' in
faas_cp_converge_roles`. Without `--roles` nothing changes: the full lane
and `make bootstrap-control-plane` still run the whole play.
`control_plane_converge.yml` converges the requested roles, or every role
when the record's environment hash differs. In lane mode it records
nothing. FaaS daemon handlers stay deferred
(`faas_join_defer_service_handlers`), as in ADR-580. Infrastructure
services restart or reload through their own handlers when their
configuration changed.

The lane is a step of the control-plane unit lane, not a separate
workflow. A unit release whose range changed a control-plane role carries
`tools/gregalectl`, built at its commit, and records the step in its
signed manifest. cd-controlplane's `converge-host-config` job runs before
`deploy-units`, which waits for it:

1. Verify the unit release.
2. Plan against the host's record. The N±1 and base checks are the same as
   for the units.
3. Run the signed gregalectl at the release commit, using the full
   release's own production manifest, read from the host and checked
   against its release manifest hash.
4. Run `deployctl restart-changed` (below) under the public-path
   observer.
5. Run the SLO gate.
6. Record.

A GitHub-hosted runner cannot resolve the fleet's private names, so the job
stops instead of skipping. The edge lane skips the job.

### Ordering

The deploy order stays data → control plane → edge/node → runtime. A host's
configuration is part of that host's stage and goes before its daemons, as
ADR-580 converges before activation. For the control plane:

1. host roles;
2. expand migrations;
3. control-plane daemons;
4. the edge lane (unchanged);
5. runtime with the next full release.

The compatibility rule mirrors expand migrations. A role change must work
with the binaries the host runs, the base release or an overlay over it.
Adding an environment variable an older binary ignores is fine. Removing
or renaming one the running binaries read, in the same change as the code
that stops reading it, is not. That change ships in two merges, or in a
full release.

### Zero downtime

`deployctl restart-changed` restarts the active daemons whose start inputs
changed since they started. Those inputs are:

- the unit and its drop-ins;
- the drop-in directory, which a removed drop-in moves;
- environment files and credentials;
- the `--config` file.

It restarts them one at a time, in restart order, each to readiness, and
holds the deploy lock. Daemons with socket units restart through them
(ADR-1736), so callers queue instead of being refused:

- apid;
- gatewayd-public;
- schedd, when `faas_schedd_listen_addr` is set.

A daemon whose only change is its `--config` file hands off instead, since
the successor reads the file again. Any other change restarts it, because a
successor inherits the old environment and credentials. A stopped daemon
stays stopped, as the roles' try-restart handlers leave it. There is never
a blanket restart. A daemon nothing changed for is not touched.

### Stateful services: the lane never restarts PostgreSQL or pgbouncer

A PostgreSQL restart drops every daemon's database connections. A pgbouncer
restart drops every pooled client. Before this lane, both happened only in
a full release, and the owner's rule is zero downtime. So the lane fails
closed on them (`host_config.stateful_services`), in three layers:

1. **Detection.** `host_roles.restart_roles` finds the roles that can
   restart or stop a stateful service:
   - a task that does so (a service or systemd module whose `name:` names
     it with `state: restarted|stopped`, `systemctl restart|stop`, or
     `pg_ctl`/`pg_ctlcluster` for PostgreSQL);
   - a task that notifies a handler, of any role in the play, that does;
   - a role that includes such a role.

   Today these are:
   - `postgres` (`restart postgresql`: listen_addresses, wal_level,
     archive_mode, removing standby settings);
   - `postgres_capacity` (restarts when the derived `max_connections` or
     `superuser_reserved_connections` moves);
   - `pgbouncer` (`restart pgbouncer`).

   A test pins this set, so a new restart path is noticed.
2. **CI and the plan.** A content change to one of these roles, or to the
   group's globals, is `stateful_restart`: it blocks both lanes and waits
   for a full release or a maintenance window. This holds for every
   `postgres_capacity` change, because whether `max_connections` moves
   depends on the inventory, which CI cannot see. `plan_host_config.py`
   checks the same at deploy time against the host's full release.
3. **The converge path.** In lane mode gregalectl passes
   `faas_stateful_restart_allowed: false`, and `control_plane_converge.yml`
   refuses lane mode without it. Each task that can lead to a restart runs
   in check mode, so a needed change is reported but `postgresql.conf` and
   `pgbouncer.ini` stay untouched and no restart is left pending. A refusal
   handler on the restart topic fails the run. The restart handlers and
   `postgres_capacity`'s restart task do not run. `postgres_capacity`
   compares the running `max_connections:superuser_reserved_connections`
   with the derived target before it writes anything, and fails on a
   difference. This covers the cases where the lane converges every role:
   no record, other globals, or an operator variable or inventory change.
   A change needing no restart (pg_hba reloads) still applies. One that
   needs a restart fails the run, which rolls back and keeps `deploy-units`
   from running.

A full release and `make bootstrap-control-plane` leave
`faas_stateful_restart_allowed` unset and restart as before. The cd-compute
path that runs `postgres_capacity` on the control plane when a node joins
(`node_join_control_plane.yml`) does too.

### SLO gate and rollback

`DEPLOYED_AT` is taken before convergence, so the gate also covers
infrastructure reloads. The gate is the canary gate from ADR-1650
(`release_units.json` `gates`). It holds the platform SLOs (unit
`host-config`) and every daemon `restart-changed` restarted. Any failure
after convergence started rolls back:

- the convergence;
- a restart that did not become ready;
- an SLO breach or an inconclusive gate.

The rollback converges the same roles at the previous content: the
record's commit, or the host's full release when there is no record. It
checks that commit out in a worktree, skips roles that did not exist there,
and runs `restart-changed` again. Neither the record nor the contract
moved, so both still describe what the host runs, and `deploy-units` does
not run.

### What stays blocking, and why

- **Release units.** These are the control-plane daemon units, sockets,
  slice and tmpfiles rule that cd-controlplane bundles: 12 paths in
  `host_config.release_units`, which a test keeps equal to the bundle step.
  Every unit-lane activation reinstalls the base release's copies, so
  converging the role would not stick. These files are generated from
  `pkg/daemonunitspec`, and none of them closed the lane after the last 15
  tags.
- **Roles that can restart PostgreSQL or pgbouncer** (`postgres`,
  `postgres_capacity`, `pgbouncer`) and the control-plane play's globals.
  See above.
- **`deploy/` paths no host role reads, and bundle files no role
  installs.** These are manifests, other playbooks, drill scripts and
  example configurations.
- **Compute host roles.** They still hold the edge lane (below).

### Compute (designed here, not built)

Compute roles go through `cd-compute.yml`, which belongs to the vmmd
session, so this change does not touch it, `node_join`, `cmd/vmmd`,
`pkg/fcvm` or the drain files. The design:

- The record is `/var/lib/faas/bootstrap/compute-roles.json`, the same
  format with group `compute_nodes`. `host_roles.py` already hashes the
  compute play.
- `node_join.yml` imports `bootstrap.yml`. The compute play's role entries
  get the same selection condition under a compute variable. node_join's
  join contract (`joinBootstrapContractHash`) keeps deciding whether a node
  converges at all.
- cd-compute converges the changed roles per node: the canary node first
  with the full gate, then followers with the follower gate, as the edge
  lane rolls. On each node, `deployctl restart-changed --only` covers the
  daemons that restart without touching VMs: gatewayd-internal and
  realtimed (both hand off), and compute schedd.
- A role change that touches vmmd's start inputs needs the vmmd
  restart-adoption work, or the drain path, before it can ride a lane.
  `vmmd_service` is classified drain-required until then.
- Once compute converges in a lane, `compute_deploy` stops holding the edge
  lane, in the same way `host_config` stopped holding the control-plane
  lane.

## Consequences

- **Lane-open length.** In the simulation over the last 15 tags (migrations
  taken as lane-eligible, horizon today's main), the control-plane lane
  stays open for a median of 29 merges instead of 3. Without the
  stateful-restart rule it would be 64. The remaining closers in that
  window are:
  - the `postgres_capacity` budget changes and the `postgres` change
    (`stateful_restart`), deliberately;
  - `deploy/managed-postgres.example.json`, which no role reads;
  - the go.mod change.
- **First run.** The first lane run on a host has no record, so it
  converges every control-plane role once. Through ADR-580 that took about
  30 minutes on production-us. The PostgreSQL and pgbouncer roles run under
  the restart guard. If the host's settings drifted from them, the run
  fails instead of restarting the database.
- **Next full release.** That release's ADR-580 contract changes once,
  because the control-plane play's role entries gained the selection
  condition, so it runs the whole play once. The role hashes ignore the
  condition, so they do not move.
- **Operator variable changes.** A `CP_ANSIBLE_VARS_B64` change still
  reaches every role. The lane does that on its next run that has a record,
  and the full lane does it as before. An inventory change that moves
  `max_connections` (a compute node added) fails the lane's run. It waits
  for a full release, or for the join path that already restarts
  PostgreSQL.
- **Time budget.** A host-config step adds about 7-9 minutes on the control
  plane:
  - verification, checkout and the cached Ansible toolchain: 1-2 min;
  - facts and the changed roles: 1-3 min;
  - restarts: about 10 s per daemon;
  - the 4-minute canary gate.

  When the same release also restarts control-plane daemons and the edge,
  the 28-minute budget of ADR-1650 goes above 30 minutes. Sharing one gate
  between the host-config step and `deploy-units` is a follow-up.

## Rollout

1. Merge. Nothing runs until a unit release carries host configuration and
   autodeploy (`UNIT_RELEASE_AUTODEPLOY_ENVIRONMENT`) or a dispatch deploys
   it.
2. The first run per environment converges every control-plane role and
   records them. Run it while the owner watches. A real-host run (fsn-4 or a
   staging control plane) is owed and needs the owner's OK.
3. The next full release runs the whole control-plane play once (contract
   change).

## Still needed

- **Compute lane.** This needs a cd-compute per-node converge with
  `--roles`, the compute play's selection condition, `restart-changed`
  per node, and vmmd-touching roles kept drain-required. It is owned by the
  vmmd session.
- **The full lane writes the record.** When the tag carries
  `scripts/ci/host_roles.py`, cd-controlplane's ADR-580 step should record
  the role hashes after activation, so the lane's first diff after a full
  release is exact.
- **ADR-580's contract misses role inputs outside `deploy/ansible`.** For
  example, `deploy/systemd/faas-pg-*`, `deploy/scripts/*`, the status page
  and `docs/DPA.md`. The role hashes cover them, and the contract should
  too.
- **Release units ride the unit lane.** The overlay would carry `systemd/`
  and `tmpfiles.d/` built at the release commit, and deployctl would restart
  the services whose unit changed.
- **More trees outside releases.** Operator playbooks (`cp_state_restore`,
  `postgres_promote`, standby), drill scripts and `deploy/lima`, each with
  proof that no release reads it.
- **Planned stateful restarts.** A maintenance-window mode would let a
  dispatched lane run allow the restart (`faas_stateful_restart_allowed`)
  behind the owner's approval. Until then, those changes take a full
  release.
- **One gate when host configuration and units restart together.**

## Rejected alternatives

- **Unit files first.** Measured: no control-plane closer was a unit-file
  change.
- **Run the whole ADR-580 play in the lane.** That is about 30 minutes, and
  it restarts infrastructure for roles that did not change.
- **Generate a role-filtered playbook, or use `--tags`.** A generated
  playbook is a second runner that drifts from `bootstrap.yml`. Role tags
  do not reach tasks of a role included at run time.
- **Path-based selection without hashes.** It cannot see a revert, and it
  cannot say what a host last converged.
- **Record only a commit.** The hashes make the record self-describing
  across changes to the hash format and to references outside
  `deploy/ansible`.
- **A separate host-config workflow.** It would race `deploy-units` on
  order and need a second signed artifact for the same commit.
