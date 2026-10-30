# ADR 20261027151755 · Unit lanes: one class per deploy/ path

- **Status:** proposed
- **Date:** 2026-10-27
- **Amends:** ADR-1650 (per-unit control-plane releases), ADR 20261018133100
  (host-configuration lane)
- **Related:** ADR 20261018084150 (expand-only migrations in per-unit
  releases), ADR 20261020214632 (safety rails for automatic unit deploys),
  docs/release-compatibility.md

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

`release-units.yml` builds a unit release on every merge to main, but no
unit release has reached production. Two things stop it:

- The repository variable `UNIT_RELEASE_AUTODEPLOY_ENVIRONMENT` is unset, so
  no push run deploys.
- Eligibility is cumulative. A unit release is an overlay on the full
  release the hosts run (ADR-1650 rejected per-merge, non-cumulative unit
  releases: a skipped or failed deploy would leave a later one unable to say
  what the host runs). So every path changed since the base tag counts, and
  one blocking path closes both lanes for every later merge until the next
  full tag. A class that blocks files no host reads costs the whole window.

**What closed the lanes after rc.297.** `affected_units.py` over
`v0.1.18-rc.297..1d3ad6e8e6` (91 merges) blocked both lanes from the first
merge. These are the `deploy/` paths the range changed and how each was
classified:

| Path | Merge | Before | Now |
| --- | --- | --- | --- |
| `roles/control_plane_service/files/faas-apid.service` | #5601 (1st) | release_units, blocking | release_units, blocking |
| `manifest/production/gcp-live.template.yaml` | #5561 (13th) | deploy, blocking | comment lines only: no flag |
| `ansible/node_join.yml` | #5636, #5653 | deploy, blocking | compute_only: pending, holds the edge lane |
| `group_vars/compute_nodes/vmmd_adoption.yml` | #5653 | compute_deploy | compute_deploy (a compute global) |
| `roles/gatewayd_internal_service/files/faas-realtimed.service` | #5601 | compute_deploy | compute_deploy |
| `roles/vmmd_service/files/vmmd.toml.example` | #5570 | compute_deploy | compute_deploy (the role installs it) |
| `roles/seal_domains/{defaults,tasks,templates}/…` | #5677 | host_config + compute_deploy | unchanged |
| `roles/prometheus/files/faas.rules.yml` | #5573 | host_config | host_config |
| `roles/grafana/files/frozen-tier.json` | #5570 | host_config | host_config |
| `roles/seal_domains/README.md`, `manifest/README.md` | #5592, #5677, #5561 | no flag (docs) | inert |
| `grafana/frozen-tier.json`, `systemd/faas-{apid,realtimed}.service` | #5570, #5601 | no flag (outside releases) | inert |

The Prometheus rule file and the Grafana dashboard already ride the
control-plane lane: ADR 20261018133100 made every path a control-plane role
reads host configuration, converged by content hash before anything
restarts. Neither role restarts a FaaS daemon or a stateful service, nor
notifies another role's handlers. A test now holds that.

What did close the control-plane lane was a release unit (the
`FAAS_REALTIME_FAIR_USE_ENABLED=1` line #5601 added to apid's unit, 20
minutes after the tag), a comment added to the production manifest template,
and the compute join playbook, which only compute hosts run and which the
catch-all `deploy` class blocked. Without the release unit, the last two
would each have closed the lane on their own.

The catch-all is the deeper problem. `deploy` meant "any `deploy/` path that
no role reads and that no list exempts", so a new file closed both lanes by
default, silently, and nobody decided that it should.

**What closed the lane after earlier tags.** We walked main's first-parent
history from each of the 15 tags rc.282 to rc.296. For each merge we
classified its cumulative range from the tag with both the old and the new
rules. Go closures were not built, so a window counts as open while nothing
blocks it.

| Window | Merges | Open (old) | Open (new) | First closer |
| --- | --- | --- | --- | --- |
| rc.282..283 | 16 | 16 | 16 | none |
| rc.283..284 | 30 | 18 | 18 | postgres role (stateful_restart), github_actions_runner role |
| rc.284..285 | 2 | 0 | 0 | contract migrations |
| rc.285..286 | 27 | 23 | 23 | contract migration, constraint-validation finding |
| rc.286..287 | 1 | 1 | 1 | none |
| rc.287..288 | 6 | 6 | 6 | none |
| rc.288..289 | 27 | 0 | 0 | contract migration |
| rc.289..290 | 3 | 1 | 1 | go.mod, deploy/lima, deploy/packer |
| rc.290..291 | 130 | 16 | 16 | contract migration |
| rc.291..292 | 9 | 6 | 6 | `faas-schedd.service` (release unit) |
| rc.292..293 | 13 | 0 | 0 | postgres role (stateful_restart) |
| rc.293..294 | 25 | 12 | 12 | lock-fanout finding |
| rc.294..295 | 141 | 16 | 16 | contract migration |
| rc.295..296 | 52 | 8 | 8 | inventory/hosts.ini, pki_renew.yml, site.yml |
| rc.296..297 | 45 | 6 | 6 | contract migration |
| rc.297..main | 91 | 0 | 0 | `faas-apid.service` (release unit) |

The median window stays open for 6 merges under both rules. The new classes
never move the first closer of a past window, and that is expected: those
closers were contract migrations or unwaived lint findings (7 of 16
windows), restarts of a stateful service (2), release units (2) and paths
that stay blocking (3, two of them in the same merge as another closer).
The new classes act on what comes after the first closer. On rc.297..main
with the release unit set aside, the old rules close the lane at merge 13
(the manifest comment), and the new rules leave it open for all 91 merges.

So this record makes `deploy/` stop being a closer that nobody chose. The
levers that move the median are elsewhere, under "Still needed": contract
migrations, which need a full release by design, and release units.

## Decision

**Every `deploy/` path has exactly one class.** `affected_units.py`
`deploy_class` decides it in this order, first match wins:

| Class | Decided by | Flag | Control-plane lane | Edge lane |
| --- | --- | --- | --- | --- |
| `release_units` | `host_config.release_units` (the unit files deployctl installs at activation) | release_units | blocks | blocks |
| `host_roles` | a host role reads it (`host_roles.py`) | host_config (control plane), compute_deploy (compute) | converges / pending | holds* |
| `inert` | `deploy_classes.inert` | none | — | — |
| `compute_only` | `deploy_classes.compute_only` | compute_deploy | pending | holds* |
| `blocking` | `deploy_classes.blocking` | deploy | blocks | blocks |
| `unclassified` | nothing names it | deploy | blocks | blocks |

\* while the edge lane restarts a gateway on a compute host (below).

The table lives in `scripts/ci/release_units.json` `deploy_classes`. A
pattern is a directory (trailing `/`), an exact path, or a glob (`*` within a
segment, `**/` across segments).

- **inert:** nothing a host runs reads it at activation. Documentation
  (`**/*.md`), tests, samples no role reads (`*.example`, `*.example.*`:
  `deploy/etc/*.toml.example`, `deploy/managed-postgres.example.json`,
  inventory and manifest examples), `.ansible-lint`, and the trees no release
  installs: `deploy/systemd/` (deployctl generate's dev-VM tree),
  `deploy/grafana/` (the grafana role's mirror) and `deploy/cloudflare/`
  (their own workflows). These replace `host_config.outside_releases`.
- **compute_only:** the playbooks only compute hosts run: `node_join.yml`,
  `node_join_accept_release.yml`, `node_join_contract_probe.yml`. Every
  play targets `compute_nodes`. The control-plane lane never runs them.
  `group_vars/compute_nodes/` and compute roles are already compute_deploy,
  because `host_roles.py` reads them as compute globals and role content.
- **blocking:** what a full release or an operator path reads and the lanes
  cannot vouch for: the production manifest template (the lanes use the
  host's full-release manifest), the other playbooks
  (`control_plane_converge.yml`, `node_join_control_plane.yml`, the standby
  and restore playbooks, `site.yml`, …), the roles no bootstrap play runs,
  `inventory/hosts.ini`, `deploy/etc/`, `deploy/scripts/`, `deploy/packer/`,
  `deploy/lima/`, `deploy/gcp/`, `deploy/firecracker/`, `deploy/nftables/`,
  `deploy/internal-mail/` and `deploy/controlplane/`. These stay as they were.
  Moving any of them needs the proof in "Still needed".

A sample a role reads keeps the role's class.
`control_plane_service/files/outboundd.toml.example` is the reason:
cd-controlplane's bundle seeds `/etc/faas/outboundd.toml` from it.

**The table is complete.** `affected_units_test.py` checks every tracked
`deploy/` path on main:

- each path has a class other than `unclassified`, so a new tree or playbook
  is classified in the change that adds it rather than closing both lanes by
  default;
- every pattern still decides some path, so no pattern is dead;
- no release workflow names an inert path or tree;
- the classes decided above are pinned.

At run time an unclassified path still blocks, so the lanes fail closed.

**Comment-only changes are inert.** A path no host role reads and that is not
a release unit maps to no flag when its change since the base only touches
blank lines and `#` comment lines. That holds for YAML, INI, TOML, cfg,
conf, nft and shell files; a `#!` line counts as content. The comparison is
of the base and head contents, so it sees reverts. Role files are excluded
because Ansible renders their comments onto hosts and notifies handlers.
Unit files are excluded because the release units stay blocking whole.

**The edge lane waits for compute paths only when it restarts a gateway on a
compute host.** It used to wait on any compute_deploy path. It now waits
when a binary it carries has a `compute-only` service in `release_units.json`.
Today both gateways do (`gatewayd-internal`, `gatewayd-public-edge`), so the
behaviour is unchanged. A gateway that runs only on the control plane would
no longer wait for compute convergence it never needs.

**Unchanged:** release units, go.mod and go.sum, the blocking class,
stateful restarts, migrations that are not lane-eligible, the release
tarball's contents and the bundle files no role installs all still block.
Selection, the data step, host-config convergence, gates and watch are as
before.

## Consequences

- **rc.297..main.** The control-plane lane is now closed by one path,
  `faas-apid.service` (#5601), which this record keeps blocking. With
  `--base v0.1.18-rc.297 --head origin/main` the report says
  `release_units (blocking): 1 paths` and nothing else for that lane. The
  edge lane also waits on compute_deploy: `vmmd_adoption.yml`,
  `node_join.yml`, `faas-realtimed.service`, `vmmd.toml.example` and the
  seal_domains files. The lanes reopen after the next full tag.
- **Report.** `affected_units.py` adds `deploy_classes` (count and paths per
  class) and a `deploy/ classes:` summary line. Consumers that read `flags`,
  `lanes` and `blocked_by` are unaffected.
- **A new deploy/ file outside the table fails ci-light** with a message
  naming the file. Files under a directory pattern need nothing.
- **Autodeploy is still off.** Setting `UNIT_RELEASE_AUTODEPLOY_ENVIRONMENT`
  to `production-us` is the owner's step, after this merges.

## Still needed

- **Contract migrations and migration findings** closed 7 of the 16 windows
  above. A contract migration needs a full release by design (ADR
  20261018084150). Splitting such changes into an expand migration that
  ships first and a contract migration that ships at the next tag would
  keep the lane open. That is a review norm, not a lane change.
- **Release units ride the control-plane lane.** This is the only closer left
  on rc.297..main. The overlay would carry `systemd/`, `optional-systemd/` and
  `tmpfiles.d/` built at the release commit, and deployctl would install them,
  run daemon-reload and restart the services whose unit changed (ADR
  20261018133100, "Still needed"). It touches deployctl and cd-controlplane.
  On rc.297..main this alone would have kept the lane open for 91 merges.
- **Compute roles converge in a lane.** Until then, compute_deploy holds the
  edge lane (ADR 20261018133100, "Compute").
- **More inert trees, each with proof that no release reads it.** Candidates
  are `deploy/lima/`, `deploy/packer/`, `deploy/firecracker/experiments/`, the
  operator playbooks (`cp_state_restore`, `postgres_promote`, standby) and
  `deploy/scripts/` drills.

## Rejected alternatives

- **Per-merge eligibility.** ADR-1650 rejected non-cumulative unit releases,
  and that reasoning still holds.
- **Keep a catch-all default.** That is the status quo. It blocks new files
  silently, and nobody owns the decision.
- **Make the production manifest template pending.** The lanes do converge
  with the host's full-release manifest, but a daemon or role change may
  depend on the new value. That is the same reason release units stay
  blocking. The comment-only rule covers today's case without that risk.
- **Treat every sample as inert, even one a role reads.**
  `outboundd.toml.example` seeds a real config file through the control-plane
  bundle.
- **Ignore comment changes in role files too.** Rendered files carry their
  comments onto hosts, and a changed file notifies handlers.
