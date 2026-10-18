# ADR-1650 · Per-unit control-plane releases

- **Status:** proposed
- **Date:** 2026-10-17 (slice 2: 2026-10-18)
- **Related:** ADR-113 (canonical signed release), ADR-129 (deploy version gauge), ADR-580 (control-plane convergence in CD), ADR-20261018072701 (zero-downtime restarts), ADR 202610171900 (release compatibility gates)

## Context

One signed tag (release.yml) ships every daemon, CLI, runner and the kernel
(ADR-113). Building and signing takes 4-5 minutes, but a rollout takes 55-75
minutes: the mega-tier gate, base prestage, compute drains and acceptance all
run for every release, whatever it changed. A merge that touches one
control-plane daemon waits for all of it.

cd-platform run 38985388820 shows where the time goes:

- In the control-plane stage, 30.7 of 43.3 min was the full Ansible
  convergence of the control-plane roles, which runs whenever their inputs
  changed since the last release, and nearly every release changes some.
- Each compute node took 22-28 min, serially. About 720 of its 752 Ansible
  tasks were small convergence tasks (about 12 min), and the runtime-base
  pre-stage took 5.0 min.
- The fleet convergence report took 16-20 min.

Activating new binaries is entangled with configuring the host. The owner's target is a merged change
in production within 30 minutes, deploying only what it touches, with zero
downtime.

The control plane has no vmmd. Its daemons (apid, meterd, githubd, outboundd
and the optional s3-gatewayd and bridged) restart without touching customer
VMs, and apid is socket-activated, so a restart queues rather than refuses
connections.

## Decision

Add a second, narrower release lane for control-plane daemons. Full releases
are unchanged and stay the only lane for everything else.

**Selection (`scripts/ci/affected_units.py`).** A binary's inputs are the files
`go build` reads for it under the release configuration (linux/amd64, CGO off,
`-tags metal` for daemons): the Go, assembly and embedded files of every module
package in its import closure, from `go list -deps`, plus go.mod, go.sum and
the Makefile's release build recipe. Test files and files a build constraint
excludes are not inputs. Each binary gets a **unit version**: the newest
first-parent commit between the base full release and the head that changed one
of its inputs. Lanes come from `scripts/ci/release_units.json`, which a test
keeps in lockstep with `pkg/daemonunitspec` roles, units and readiness URLs.

A range is eligible when at least one control-plane daemon changed and none of
these did since the base: migrations, `deploy/` (units, roles, rules,
manifests), go.mod/go.sum, the files cd-controlplane copies into its bundle,
and the release tarball's own contents. Roles only the compute play runs
(`compute_only_paths`, which a test checks against bootstrap.yml) wait instead
of blocking. Blocking go.mod also means a unit release never changes a
third-party dependency, so the release SBOM and its KGV baseline still
describe the control plane. Shared daemons (schedd and
gatewayd-public also run on compute hosts), compute daemons, CLIs, guest and
image inputs do not block. They are reported as pending and wait at the base
release, which is the mixed state every rolling rollout already runs while
compute trails the control plane.

**Build (`release-units.yml`).** On each push to main, build every eligible
control-plane daemon changed since the newest full release tag. The build uses
`make build-release-unit`, which has the same flags as `build-release-batch`, and
stamps `pkg/wire.Version=unit-<unit version>`. The bytes therefore depend only
on the source the daemon was built from. The workflow signs the tarball with
GitHub OIDC (identity: release-units.yml on main) and uploads it as an artifact.
A unit release is cumulative over its base, so a newer one supersedes an older
one.

**Deploy (`cd-controlplane.yml`, `deploy-units` job, `units_run_id`).**

1. Verify the signature, the manifest and each binary's digest.
2. Read the host's active release. The plan (`scripts/ci/plan_unit_deploy.py`)
   refuses unless the host runs exactly the unit release's base full release,
   or an earlier unit release over it. It restarts only daemons whose unit
   version differs from the host's.
3. `deployctl unit-overlay` assembles a new immutable release directory under
   the deploy lock. Each file is a hard link to the active release, except the
   replaced binaries, `bin/deployctl` and `unit-versions.json`. The id is
   deterministic (40 hex), so a rerun reuses the directory and retention and
   rollback treat it like any release.
4. `deployctl deploy --only <services> --expect-current <base>` activates it.
   The controller checks the active release under the lock (compare-and-swap),
   then restarts and gates only the listed services. It rolls back to the base
   on failure.
5. `scripts/unit-release-verify.sh` checks each unit (active, running from the
   new release, readiness 200) and restarts enabled optional units onto it. On
   failure the workflow runs `deployctl rollback --only` and the units are back
   on the previous release. All of this runs under the same public-path
   availability observer as a full activation.

The unit lane never runs migrations, role convergence, KGV rotation or
fleet-seal steps. Those inputs are blocking, so they are unchanged since the
base, which already ran them. This is the split between release activation
and host configuration, for the control plane: no Ansible runs on the unit
lane's critical path.

## Consequences

- A single-daemon merge reaches the control plane in about 10-15 minutes,
  opt-in through `UNIT_RELEASE_AUTODEPLOY_ENVIRONMENT`. No full tag is cut,
  and compute is untouched.
- Daemons on the control plane can report different versions. Full releases
  still converge to one version, and `FaasDeployVersionSkew` now ignores
  `unit-*` versions.
- The next full release replaces every overlay. Overlays need no extra disk,
  because unchanged files are hard links. Retention keeps the active
  overlay's base full release however many overlays follow it. That
  directory holds the canonical binaries `gregalectl doctor` (and so
  `fleet_verify`) verifies, and it stays a full rollback target.
- The unit lane's gate is the merged pull request's light tier. The mega tier
  still gates every full tag and runs nightly. This is a deliberate trade the
  owner may tighten, for example by requiring a green nightly on an ancestor.
- Measured on the last 300 merges, each taken on its own: 47% were eligible
  (7% touched only control-plane daemons, 40% also touched shared or compute
  daemons, which wait), 27% were blocked (18% migrations, 12% `deploy/`), and
  26% changed no control-plane binary. Unit releases accumulate since the
  base, though, so one blocking merge closes the lane until the next full
  release. After each of the last 15 tags the lane stayed open for a median of
  1 merge (range 0-8). Slice 1 is mechanically complete but rarely usable
  until migrations stop blocking.

## Slice 2: SLO gate, the edge lane, order and skew

**SLO gate.** After a unit lane activates a host, `scripts/ci/unit_deploy.sh
gate` runs `scripts/ci/slo_gate.py` (ADR 202610171900) for each restarted
unit, in parallel on the control plane, where Prometheus listens at
`127.0.0.1:9095`. The gate length depends on the host's place in its lane
(canary first, below). `--deployed-at` is the control plane's clock right
before activation. A pass (0) promotes the
units. A breach (1), an inconclusive result (3) or a misconfigured gate (2)
runs `deployctl rollback --only` on the plan's services, which restores the
release that was active before them, and fails the run. A converge run (a
rerun after an interrupted deploy) gates again. Gating per unit keeps each
unit's own "still scraped" check.

**Edge lane.** gatewayd-public and gatewayd-internal get the lane `edge` in
`release_units.json`. Each of their services names the host role it runs on:
gatewayd-public on the control plane and, where a cell enables it,
gatewayd-public-edge on compute; gatewayd-internal on compute. The edge
lane is blocked by everything that blocks the control-plane lane, and also
by changes to roles only compute hosts run, because it lands on compute
hosts without converging their roles. `cd-edge-units.yml` runs it:

1. Verify the artifact. Refuse unless the control plane already runs the
   unit release's control-plane units.
2. Deploy the canary node (cd-edge-node.yml), with the full gate.
3. Deploy the control plane's gatewayd-public, through cd-controlplane.yml
   with `units_lane=edge`.
4. Deploy each remaining compute node in turn (`max-parallel: 1`,
   `fail-fast`), with the follower gate. A node's run shares cd-compute.yml's
   per-node concurrency group, so a full release never changes the same node
   at the same time.

On each node the job plans the deploy, assembles a hard-linked overlay, runs
`deployctl deploy --only gatewayd-internal`, checks every unit, restarts an
enabled gatewayd-public-edge onto the new binary, and gates the node. A
failed node, canary or follower, is rolled back. The lane stops there and
the remaining nodes are left as they were.

**Canary first.** Each lane's first host is its canary and gets the full
gate. Every later host gets the follower gate, which is long enough to catch
a crash loop or an immediate error spike. The gate lengths live in
`scripts/ci/release_units.json` under `gates`:

| gate | duration | max hold | why |
|---|---|---|---|
| canary | 4 min | 2 min | the first host to run the new code |
| follower | 90 s | 30 s | 15 s rule evaluation + 30 s hold leaves a margin |

`scripts/ci/plan_edge_rollout.py` picks the canary:

- *Control-plane lane:* the control plane, its only host.
- *Edge lane:* the designated compute node (`edge_canary`, or the
  environment variable `UNIT_RELEASE_EDGE_CANARY`; default the first edge
  target). The control plane's gatewayd-public follows it.
- *gatewayd-public-only release:* no compute node restarts a gateway
  (gatewayd-public-edge is optional), so the control plane is the edge
  lane's canary and every node follows.

**Signed deployctl.** The unit release now carries `tools/deployctl`, built
in release-units.yml from the release commit and covered by its signature.
Deploy jobs run it instead of building their own, so they need no Go
toolchain and save about half a minute each.

Compute nodes run releases installed by `gregalectl release install`, which
ship no deployment manifest and no systemd units. Two changes cover that:

- `deployctl unit-overlay` gives the base release a deployment manifest
  built from exactly its files, without the host's SBOM sidecar. The overlay
  can then inherit its hashes, and the base verifies as a rollback target.
- `deployctl deploy` on a release without units requires `--only`. It checks
  that each service is installed on the host and that its binary is in the
  release, installs no units, and runs no migration.

**Dependency guard.** systemd starts a restarted unit's
`Wants=`/`Requires=`/`BindsTo=` units that are down, and holds the restart
until they finish starting. faas-gatewayd-internal Wants faas-vmmd. An edge
deploy on a node whose vmmd is stopped (a maintenance, a drain) would
therefore start vmmd, and wait on it. `deployctl deploy --only` refuses,
before changing anything, while such a dependency is neither active nor
masked. A rollback is not guarded: recovery takes priority, and the deploy
it undoes already passed the guard.

**Restart or reload.** deployctl decides how a service restarts. With
ADR-20261018072701's two-generation hand-off (#4898), it reloads a gateway when both the
running and the new binary support the hand-off and nothing the unit read at
start has changed, and restarts it otherwise. The lanes call `deployctl
deploy --only` and inherit that choice.

**Order and skew.**

- *N±1:* every lane refuses a unit release that is not built over the newest
  full release tag before its commit. That is the release CI compared each
  merge against, so mixed units are at most one release apart.
- *Order:* migrations block the unit lanes, so data changes go out only in a
  full release. `release-units.yml` deploys the control-plane lane, then the
  edge lane. The edge lane checks the control plane itself when it is run
  alone. Runtime (guest, images) waits for a full release.

The `base` dispatch input can no longer move a unit release onto an older
production release. That combination is exactly what N±1 forbids.

## Time budget

The target is a release that changes both lanes, deployed to the control
plane and three compute nodes, promoted within 30 minutes of the merge,
gates included. Minutes per phase:

| phase | min | basis |
|---|---|---|
| release-units build (hosted runner, main-branch cache) | 4.5 | checkout, cache restore, detector (3 s locally), build (22.5 s warm locally for apid, both gateways and deployctl), sign and upload |
| control-plane lane: deploy + canary gate | 2 + 4 | per-host deploy job: artifact download and verification, checkout, plan, stage, overlay, restart and readiness |
| edge plan job | 1 | artifact verification, control-plane order check, canary choice |
| edge canary node: deploy + canary gate | 2 + 4 | |
| control plane gatewayd-public: deploy + follower gate | 2 + 1.5 | |
| two more nodes: deploy + follower gate each | 2 × (2 + 1.5) | |
| **total** | **28** | |

`scripts/ci/plan_edge_rollout.py --budget` computes this from the gate
config, and a test fails if it exceeds 30 minutes. The per-host costs are
estimates from the steps' local and slice-1 timings, not production
measurements. A queued hosted build runner (0-14 min on busy CI days) comes
on top. Each extra compute node adds 3.5 minutes.

## Real-host evidence (fsn-4, 2026-10-18)

The owner approved a run on fsn-4 (faas-compute-node-4). The run used the
node's own `gregalectl release install` release (v0.1.18-rc.198, 1.2 GB, 31
files), its real faas-gatewayd-internal unit, and deployctl and the host
scripts from this branch. fsn-4's own gateway cannot reach a database, so
stub gateway binaries served readiness on the real control port. Five runs,
each restoring the host on exit:

1. **Bug: `--only` was ignored on host-managed releases.** On a release
   without unit files, `--only` was ignored, and a gatewayd-internal deploy
   restarted vmmd first. Fixed, with a regression test.
2. **Bug: rollback could not find deployctl.** The post-gate rollback used
   the active release's deployctl, which a full compute release does not
   ship. It now uses the staged copy.
3. **Gap: dependencies were started by systemd.** Restarting
   gatewayd-internal while vmmd was down made systemd start vmmd and hold
   the restart behind it. This led to the dependency guard above.
4. **Bug: the guard read unit states by position.** systemctl prints
   properties in its own order, so the guard would have refused every
   deploy. It now parses them by key.
5. **All phases pass:**
   - the guard refuses while vmmd is down, before any change;
   - a deploy over the base and one over an overlay each restart only
     gatewayd-internal;
   - a rerun restarts nothing;
   - the post-gate rollback restores the previous overlay;
   - a broken binary rolls back by itself;
   - the base's binaries stay untouched and unchanged daemons are hard
     links;
   - no other faas unit was restarted in any phase.

On-host times:

| step | time |
|---|---|
| first overlay on a node (hashes the base once) + refusal | 24-48 s |
| deploy over the base | 10.5 s |
| deploy over an earlier overlay | 7.2 s |
| converged rerun | 3.4 s |
| post-gate rollback | 7.2 s |
| broken binary to automatic rollback | 10.9 s |

These sit inside the budget's 2-minute per-host deploy. The first deploy on
a node pays its one-time hashing.

## Still needed

- **Migrations.** Most blocked ranges contain a migration. Allowing the lane to
  run expand-only migrations ahead of restarts needs a machine-checkable
  compatibility marker (owned by the release-safety-rails work).
- **Compute daemons beyond the gateways.** schedd and realtimed restart
  without touching VMs and could join the edge lane once their restarts are
  covered (ADR-20261018072701). vmmd needs the restart-adoption work.
- **Edge target inventory.** The edge lane trusts `edge_targets` or
  `UNIT_RELEASE_EDGE_TARGETS` to list the compute nodes. A node left out stays
  on its current gateways, which N±1 allows. Reading the node list from the
  compute registry would make the lane complete by construction.
- **Measure the budget.** Replace the per-host estimates with production
  timings from the first runs. Past about five nodes, gate waves of nodes
  instead of one node at a time.
- **Build runner.** release-units.yml builds on a GitHub-hosted runner, so on
  a busy CI day its job can queue 8-14 min before it starts. Building on the
  dedicated fleet runner (warm Go cache, no queue) keeps merge-to-prod near 10
  min, but puts builds of main on the host that holds deploy keys. That is the
  owner's decision.
- **Full lane, outside this ADR.** Run host configuration in its own lane,
  keyed by a per-role content hash, instead of inside every activation.
  Content-address runtime bases and pre-stage them off the critical path.
  Roll compute as a canary node, then parallel waves, once activation is
  per-unit without a drain.
- **Base discovery.** The base is the newest tag. A production that trails
  it must roll that full release out before unit releases resume.

## Rejected alternatives

- **Faster full releases only.** Every release still drains compute and runs
  acceptance for code it did not change.
- **Swap a binary in place under `/opt/faas/current`.** This breaks
  immutability, bundle verification and rollback, and a crash restart could
  pick up half-written bytes.
- **Per-merge, non-cumulative unit releases.** A skipped or failed deploy
  would leave a later one unable to prove what the host runs.
- **Stamp the merge commit as the version.** The bytes would change on every
  merge, and a rebuild could not show the host already runs that code.
