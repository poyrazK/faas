# ADR 20261122170000 · Continuous unit lane: whole commits, under four minutes

- **Status:** accepted by the owner, 2026-11-22 (targets: a merged PR on
  production in under 4 min, ultra safe; automatic CP+edge deploys; full
  releases stay manual)
- **Date:** 2026-11-22
- **Related:** ADR-1650 (per-unit releases), ADR 20261018084150 (expand-only
  migrations in unit releases), ADR 20261018133100 (host-configuration
  lane), ADR 20261020214632 (safety rails for automatic unit deploys),
  ADR 20261027151755 (deploy path classes), ADR 20261117140000 (per-variable
  role hashes), ADR-1736 (two-generation hand-off)

## Context

The unit lane (ADR-1650) can ship a control-plane fix in minutes, but it
almost never does, so changes reach production through cd-platform
(45–95 min when clean; 6 of its last 10 runs failed) or by hand on the
hosts (drop-ins the installer later clobbers, outside signing, the SLO
watch and rollback).

Of the last 60 successful `release-units` runs (2026-11-21/22), **6 could
deploy**. 48 were refused, 6 had nothing to ship:

| why refused | runs |
| --- | --- |
| `deploy/manifest/production/` (blocking class) | 29 |
| release unit files | 14 |
| `stateful_restart` (control-plane group_vars) | 5 |

Three causes:

1. **One change refuses the whole range.** A unit release is cumulative
   over the full release the hosts run. Any blocking path merged since then
   refuses every later unit release until the next full tag, even when the
   daemons it would ship have nothing to do with that path.
2. **Every group variable looked stateful.** `host_roles.py` treated any
   mention of `hostvars` as a read of every variable.
   `postgres_capacity` (which can restart PostgreSQL) uses
   `map('extract', hostvars) | selectattr('faas_edge_on_compute_nodes')`,
   so a new `group_vars/control_plane/*.yml` for meterd made the lane
   refuse with `stateful_restart`.
3. **Nothing deploys unless someone dispatches it.** Automatic deploys are
   off; the last dispatch was 2026-11-07.

And the lane is slow when it runs: ~20 min from merge (hosted runner queue
and cold checkout, 391 s choosing units by building each one at base and at
head, 104 s building them again with the version stamp, 73 s staging, 159 s
activating, 247 s held on the SLO gate).

It also had a hole. Changed compute and shared daemons are left at base
("pending") while the control-plane daemons ship, so one PR that changes
apid and vmmd together put the new apid next to the old vmmd until the
next full release. A full rollout passes through that state too, but only
for minutes and only for daemon pairs; a unit release can hold it for days.

## Decision

The owner's two requirements: **a merged PR on production in under four
minutes, and ultra safe.** Safety comes first: nothing below ships a tree
CI did not test, splits a change, or shortens the post-deploy watch.

### 1. Whole commits (this change)

The unit of deployment is a commit of main, never a daemon or a path.

- **Prefix.** When the range from the full release to main's head is not
  eligible, the lane releases the newest commit up to which every commit is
  (`affected_units.py` `lane_prefix`): walking first-parent history oldest
  first, the first commit that carries a blocking flag on its own stops the
  lane. That commit, and everything merged after it, waits for the next
  full release. The lane never skips a commit (the result would be a tree
  nobody tested) and never parks part of one.
- **The verdict that counts is the full analysis of that commit.** The
  build job copies head's selection rules, checks the prefix commit out and
  re-runs the whole analysis there, with code selection. The release is
  built from that checkout and its manifest names that commit, so the host
  roles converge from it, the planner checks N±1 against it, the preflight
  checks that commit's post-merge light run, and the watch reads it.
- **Cross-lane commits block.** A commit that changes an input of a
  control-plane or edge daemon *and* an input only shared or compute
  daemons read (`cross_lane` flag) is one change the lane would split: it
  blocks. Inputs every lane reads (a shared package, go.mod) and tools are
  the N±1 mixed state every rollout already passes through and do not
  count. This is stricter than before: such a PR used to ship half.
- **Per-commit host roles from content hashes.** Each commit that touches a
  path a host role reads is judged by `host_roles` hashes of the commit
  against its parent, so a new group variable that no stateful role reads
  does not stop the lane.
- **A stalled lane deploys once.** While stalled, every merge releases the
  same commit; the preflight stands down when the control plane already
  runs it (`current` check; `unit_deploy.sh preflight-state` reports the
  running unit release's commit). The run summary and a warning name the
  stopping commit and how many commits wait.

### 2. Literal hostvars (this change)

`hostvars` indexes hosts. A text that reads other hosts' variables by
literal attribute (`hostvars[h].name`, `hostvars[h]['name']`,
`map('extract', hostvars, 'name')`, `map('extract', hostvars) |
selectattr('name') | map(attribute='name')`, `hostvars | dict2items |
selectattr('value.name') | map(attribute='key')`, and
`set hv = hostvars[h]` followed only by `hv.name`) names those variables
like any other reference. Every other use (a computed attribute, the whole
host dict passed on, dumped or iterated) still reads every variable. A
repository test fails when a new `hostvars` use matches none of the
shapes, so it is recognised or rewritten, not silently widened.

The deploy planner compares stateful roles between the release and the
host's full release, both hashed by the same script, and refuses on any
difference, so the narrower hashes cannot let a PostgreSQL-restarting role
into a lane deploy. Roles whose hash moves converge once (idempotent).

Replayed on 2026-11-22 (base `v0.1.18-rc.371`, main `3ccf8d4c8f`): before,
not eligible (`stateful_restart`); after, eligible: 13 control-plane
daemons, both gateways, four non-stateful roles converged first.

### 3. Under four minutes (next changes)

Budget, merge to new code serving on the control plane:

| step | now | target | how |
| --- | --- | --- | --- |
| runner pickup + checkout + cache | ~60 s | ~5 s | a dedicated build runner, main pushes only, persistent clone and Go cache |
| choose units | 391 s | ~15 s | one build: the version stamp leaves the binary (below), so the code digest is the file digest; base digests stay cached per base |
| build | 104 s | ~30 s | incremental on the warm cache, changed daemons in parallel |
| sign + hand over | ~40 s | ~15 s | cosign keyless as today; preflight runs beside the build, not after it |
| stage | 73 s | ~10 s | changed binaries only |
| activate | 159 s | ~30 s | independent units restart in parallel through the existing two-generation hand-off and pre-traffic checks |
| hold before "done" | 247 s | ~45 s | a synthetic smoke check; the 30 min watch with automatic rollback is unchanged |
| **total** | **~20 min** | **~2.5–3.5 min** | |

- **Version out of the binary.** Release builds stop stamping
  `unit-<commit>` into `pkg/wire.Version` (informational only: metrics
  label, version endpoints). deployctl writes the unit version into the
  unit's environment (`FAAS_UNIT_VERSION`) from the release manifest, and
  `pkg/wire` reads it at start. The release binary is then the neutral
  build, so one build gives both the code digest and the shipped bytes.
- **Build runner.** Main pushes only, never pull requests, so nothing a
  PR controls runs where release binaries are built. It holds no
  production credentials; the fleet runner deploys as today.
- **Rollback stays the same speed.** deployctl keeps the previous
  generation; a failed smoke check or a watch breach puts it back.

### 4. Automatic deploys

After §1 and §2 merge, the owner sets `UNIT_RELEASE_AUTODEPLOY_ENVIRONMENT`
to the production environment: every merge whose commit the lane can
release deploys the control-plane and edge lanes, behind the existing
preflight (freeze, newest, current, main's light run, idle, healthy,
watch) and the 30 min watch with automatic rollback and freeze.

### 5. Full releases stay manual

The owner cuts full releases. A stalled lane says so in every run's
summary and annotations (stopping commit, reason, commits waiting).

## Consequences

- One blocking commit still holds everything merged after it until a full
  release. The flow comes from making fewer commits blocking: §2 removed
  the largest false source; the production manifest and release unit files
  remain blocking and are the next candidates (a manifest the
  control-plane lane renders and converges like host configuration).
- A commit that changes apid and vmmd now waits for a full release instead
  of shipping half.
- Selection runs `affected_units.py` twice when the range is not eligible
  (once to find the prefix, once with code at the prefix), ~15 s more until
  §3 lands.
- Role hashes of the roles that read `hostvars` literally change once; the
  first deploy after this converges them.

## Rejected alternatives

- **Parking blocked paths and shipping the rest of each commit.** A PR that
  changes a daemon and the configuration it needs would ship the daemon
  alone: a state no full release passes through.
- **Skipping a blocked commit and releasing later ones.** The released tree
  would contain later commits without the skipped one: a tree no CI run
  tested.
- **Per-daemon releases.** Splits a PR across daemons the same way.
- **Merge queue and binaries built in the pull request.** GitHub offers
  merge queues only to organization-owned repositories (ADR
  20261020214632). A binary a PR's workflow built runs PR-controlled code
  before main signs it.
- **Building on the control plane's runner.** Release builds would take
  CPU from apid on the host that serves the API.
- **Automatic full releases when the lane stalls.** The owner keeps full
  releases manual.

## Implementation

1. This ADR; `lane_prefix`, `cross_lane`, per-commit host hashes, literal
   hostvars, the `current` preflight check, and the workflow wiring, with
   tests.
2. Version stamp out of the binary (deployctl, `pkg/wire`, the unit
   release build), one build for selection and release.
3. Dedicated build runner; preflight beside the build.
4. Parallel activation; smoke check in place of the pre-done SLO hold.
5. Owner turns on automatic deploys.
