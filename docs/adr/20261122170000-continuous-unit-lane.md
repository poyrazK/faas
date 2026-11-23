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
  same commit. Each run that releases a commit uploads a marker artifact
  named after it; the preflight's `current` check stands down when an
  earlier run of that commit finished every lane it ran (control plane,
  edge) green. A failed lane is deployed again once its deploy-freeze
  issue is closed; a release the watch rolled back is not re-shipped on its
  own (a person decides, ADR 20261020214632). The run summary and a warning
  name the stopping commit and how many commits wait, only when the whole
  range is not eligible.
- **One lane table.** The build of a prefix release reads head's
  `release_units.json`, the table its report was made with.

### 2. Literal hostvars (this change)

`hostvars` indexes hosts. A text that reads other hosts' variables by
literal attribute (`hostvars[h].name`, `hostvars[h]['name']`,
`map('extract', hostvars, 'name')`, `map('extract', hostvars) |
selectattr('name') | map(attribute='name')`, `hostvars | dict2items |
selectattr('value.name') | map(attribute='key')`, and
`set hv = hostvars[h]` followed only by `hv.name`) names those variables
like any other reference. An extract or `dict2items` chain counts only
when it ends in values (`map(attribute=...)` or `length`), so no host dict
is bound by `for`/`set` or passed on; dict methods (`.get(`, `.items(`,
`.keys(`, `.values(`) and loops over `hostvars` itself read everything. Every other use (a computed attribute, the whole
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

- **One build** (done; replaces "version out of the binary", which needed
  deployctl and daemon changes). Code builds now stamp `pkg/wire.Version`
  with `unit-` and 40 zeros and `pkg/wire.GitSHA` with 40 zeros, as long
  as a unit release's stamps, and unit release builds blank the Go build
  ID as code builds do. A release binary with its two stamps replaced is
  then the code build byte for byte (verified on meterd and apid through
  `make build-release-unit`, file digests equal). The build job builds
  the carried units once (`build-unit-release.sh` with `UNITS_BIN_DIR`,
  `UNITS_BUILD_ONLY`), `affected_units.py --code-from` measures those
  binaries, and packaging reuses them: the second build of every carried
  unit is gone. A binary without its stamp falls back to selection by
  closure (restarts more, never fewer). One-time cost: the new neutral
  stamp changes every recorded code digest, so the first unit release
  after this restarts every unit it carries, and the base digest cache
  rebuilds once.
- **Build runner, on the control plane** (owner, 2026-11-22: "if there is
  no security problem, we can use cp"). `deploy/ansible/build_runner.yml`
  installs a second runner on the control plane through the
  `github_actions_runner` role with `faas_runner_kind: build`:
  - **Only main's code.** The build job runs on pushes to main and owner
    dispatches, and refuses any other ref on this runner. No
    pull_request workflow names a self-hosted label, and fork pull
    requests need approval for every run (`approval_policy:
    all_external_contributors`). The code it compiles is the code this
    host runs as its daemons; `go build` executes none of it.
  - **Not the deployment runner.** Its own user (`faas-builder`), home,
    install root and unit; no production secrets, no access to the
    deployment runner's home (where deploy keys live between jobs), no
    private fleet endpoints. The role refuses a build runner with a fleet
    label and a deployment runner without exactly one, so neither takes
    the other's jobs.
  - **Below the services.** CPU weight 10 and `CPUQuota=400%` (4 of the
    Ryzen 5600X's 12 threads), IO weight 10, `MemoryHigh=6G`,
    `MemoryMax=8G`, `GOMAXPROCS=4` (which also bounds `go build -p`),
    `ProtectSystem=strict`. Its Go build cache is trimmed by
    `go_cache_retention` at 70 % disk use (down to 60 %), well before
    PostgreSQL's disk fills. It never touches the deployment runner's
    `/etc/hosts` fleet block.
  - **Switched by a variable.** `UNIT_RELEASE_BUILD_RUNNER` (its labels as
    JSON) moves the build job to it; unset, the job runs on a
    GitHub-hosted runner with the restored caches, as before. The runner
    bootstrap and its role are operator paths no release runs, so they are
    `inert` in the deploy classes (the deployment runner's playbook stays
    `blocking`: a release workflow names it).
- **Preflight beside the build** (done). The preflight job no longer
  needs the build: it starts with the run, so its runner pickup,
  checkout, SSH setup and first round overlap the build, and any wait
  for production (freeze, idle, healthy, watch cap) happens while the
  build runs. `deploy_preflight.py --follow-build` adds a leading `built`
  check: it reads this run's build job every 10 s, and once the build
  succeeds takes the released commit from the build's marker artifact
  for the `current` and `main` checks. Other checks are reread every
  60 s while the build runs, and the round that sees the build end
  rereads every check, so a go is never staler than that one round. A
  build that fails, is cancelled by a newer build, or releases nothing
  stands the preflight down; a freeze holds it without waiting for the
  build. Because a preflight now runs for every deploying run, `newest`
  counts a newer run as taking over only once its build released a
  commit (its marker), and ignores build-only runs (preflight skipped).
  The preflight holds a fleet runner slot while the build runs; the
  fleet runner host keeps a second instance (`faas_runner_instances`)
  for another run's lanes, as it already needed for a preflight waiting
  on one.
- **Parallel activation** (done). `deployctl deploy` restarted the
  plan's daemons one at a time in `daemonunitspec.RestartOrder`. A unit
  deploy now restarts them in waves: `daemonunitspec.RestartDepth` gives
  each daemon one more than its deepest `Lifecycle.After` dependency, and
  daemons of equal depth (no ordering path between them) restart
  together through the same per-daemon path (preload, hand-off or
  restart, readiness), at most `release_units.json` `activation.parallel`
  (4) at once. The next wave starts only when every daemon of this one is
  ready; a failure lets the rest of its wave finish, stops, and the
  activation script rolls back as before. vmmd always restarts alone:
  its `RuntimeDirectory=faas` re-creates `/run/faas` as root:root, which
  deployctl repairs before the next daemon binds there. The cap reaches
  deployctl as `DEPLOYCTL_RESTART_PARALLEL` on the deploy line only: a
  deployctl from before this change ignores it, rollbacks and full
  releases stay one at a time, and `activation.parallel: 1` turns it off.
- **No host-config job without a role change** (done). cd-controlplane's
  converge-host-config ran for every control-plane unit deploy and, with
  no role changed, downloaded and verified the release only to plan
  "none" (120 s ahead of deploy-units on 2026-10-21). release-units now
  passes `host_config_changed: false` when its report names the
  control-plane host group and no role of it changed since the base full
  release (releases are cumulative from that base, so the host then runs
  the roles that release converged), and the job is skipped. No host
  view, any changed role, a direct dispatch or a rollback still runs it.
- **Rollback stays the same speed.** deployctl keeps the previous
  generation; a failed smoke check or a watch breach puts it back.

### 3a. Safety: a unit release must not break production (owner, 2026-11-22)

No pipeline can promise a release never breaks production. The goal is
that a broken release rarely gets through, and is rolled back within
minutes, touching little, when one does. Existing rails (ADR
20261020214632): signed releases from main only, expand-only migrations
and N±1 checks, the preflight, pre-traffic checks on the two-generation
hand-off, the per-host SLO gate (canary 4 min, followers 90 s), and the
30 min watch that rolls every host back and freezes deploys. Added here:

- **Tested as shipped** (owner chose "run shipped tests"). There is no
  merge queue, so a squash merge can land a tree no CI run saw. Before a
  release is built, `scripts/ci/tested_tree.py` checks the released
  commit: if its pull request's final head held main's previous commit,
  has the same tree, and passed ci-light, CI tested exactly what ships.
  Otherwise the build job runs the tests ci-light would select
  (`affected_packages.py` test set and the large packages' related tests)
  for what CI did not see together (the other pull requests that landed
  meanwhile, and the commit's own change), limited to packages the shipped
  daemons build from, at the released commit. When ci-light passed on the
  pull request's head, only packages affected by both sides run: the head
  tested the commit's own change over an older main, main tested the other
  merges, so a package only one side affects has inputs CI already saw
  together (first merge after #7864, 2026-11-22: 710 s on a cold hosted
  runner, 4 of 5 package runs were of that kind). The release is built only
  when they pass. These tests get no database URL (on the control plane
  it would be production's), so PostgreSQL-backed tests skip there; main's
  light run and the watch still cover them.
- **Blast cap** (owner chose it). A release carries at most 5 first-parent
  commits beyond the commit production last took through every lane
  (`scripts/ci/last_deployed.py`, from the release markers; the full
  release when there is none). The rest goes in the next run: a merge, or
  a 10-minute schedule that runs only with automatic deploys on. A commit
  production already took is not built again.
- **Config rollback** (owner chose it). The watch's rollback put binaries
  back but not host configuration a lane converged. Now, before the
  host-config step converges, the watch record notes the configuration
  commit the control plane had (`host_config.known_good`, kept across
  runs) and the one converged (`current`); the watch judges the platform
  SLOs for it beside the units. On a breach, `unit_watch.py` names
  `known_good`, and cd-unit-watch's `restore-host-config` job runs
  cd-controlplane with `host_config_rollback_to`: `plan_host_config.py
  --rollback-to` converges only roles whose content differs (never a
  stateful one; other globals refuse), with the same secrets, gregalectl
  converge, daemon restarts and SLO gate, records the known-good commit,
  and deploys nothing else. A failed converge-back is not converged
  "back" again; deploys stay frozen by the watch's issue.
- **Critical smoke checks** (owner chose them). Right after the control
  plane activates a unit release, `scripts/ci/unit_smoke.py` drives the
  business paths through the public API with a dedicated smoke account:
  the account with an active plan, the smoke app, a configuration change
  (`SMOKE_TOKEN`, unique per run) applied by a fresh restart and served by
  the app's public URL (apid's write path, schedd, vmmd's cold boot, wake
  and the gateways, with no build), and a usage projection with no
  negative figure. A failure rolls the units back at once, as the gate
  does: the SLO gate sees errors and latency, not a wrong answer served
  with a 200. Full journeys build apps and take minutes, so they stay in
  the journey suite. Setup is the owner's (`tests/unit-smoke-app`);
  unconfigured, the step warns, and automatic deploys should not be turned
  on until it is configured.
- **Rollback drill** (owner chose it). Every Monday,
  `unit-rollback-drill.yml` checks, read-only, every fact an automatic
  rollback depends on: the active release and deployctl's activation
  history agree, the release a rollback restores is installed, so is the
  watch record's known-good release, the host-config record exists,
  Prometheus answers, the freeze label exists. A failure opens an issue.
  Breaking production to prove it recovers was rejected: that is the
  outage the lane exists to prevent.
- Not chosen: forcing risky paths (billing, metering, auth, limits, the
  scheduler state machine) behind flags.

### 4. Automatic deploys

After §1 and §2 merge, the owner sets `UNIT_RELEASE_AUTODEPLOY_ENVIRONMENT`
to the production environment: every merge whose commit the lane can
release deploys the control-plane and edge lanes, behind the existing
preflight (freeze, newest, current, main's light run, idle, healthy,
watch) and the 30 min watch with automatic rollback and freeze.

**Watch cap** (owner, 2026-11-23). A deploy joins an open watch, but a host
may hold only `watch.max_unwatched` releases not yet watched clean; past it
the preflight waits for the watch (up to 30 min). At 2, a merge burst queued
its third release behind the watch, past the four-minute target. The cap is
now 4 and release retention keeps 5 previous releases
(`releaseretention.DefaultKeepPrevious`, was 3), so a breach still finds the
known-good release installed; a test keeps the cap below retention. Cost:
two more releases on disk per host, about 4 GB.

**Base check** (2026-11-23). The build takes the newest tag as the base
full release, but a tag (rc.374) can exist before cd-platform deploys it.
The planner then refuses the lane (N±1) and the failed lane freezes
deploys. The build now also uploads `unit-release-base-<sha>`, the
preflight reads the full release the control plane runs (`preflight-state`
`base`), and on a mismatch it stands down green: "deploy that release with
cd-platform first". Unknown on either side leaves it to the planner.

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
- **Building on the existing deployment runner.** Its user keeps deploy
  keys in its home between jobs; a separate, capped build runner keeps
  compiles away from them and from the CPU PostgreSQL and apid need.
- **Automatic full releases when the lane stalls.** The owner keeps full
  releases manual.

## Implementation

1. This ADR; `lane_prefix`, `cross_lane`, per-commit host hashes, literal
   hostvars, the `current` preflight check, and the workflow wiring, with
   tests.
2. One build for selection and release (neutral stamps, done).
3. Build runner on the control plane (role kind, playbook, workflow switch:
   done); the owner bootstraps it and sets `UNIT_RELEASE_BUILD_RUNNER`;
   preflight beside the build (done).
4. Parallel activation (done); smoke check in place of the pre-done SLO
   hold.
5. Owner turns on automatic deploys, in the order of
   [docs/runbooks/ContinuousUnitLane.md](../runbooks/ContinuousUnitLane.md).
   An automatic deploy refuses to start until the critical smoke checks are
   configured (the preflight's "Require the critical smoke checks" step).
