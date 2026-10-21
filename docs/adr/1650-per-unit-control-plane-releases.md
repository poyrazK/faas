# ADR-1650 · Per-unit control-plane releases

- **Status:** proposed
- **Date:** 2026-10-17 (slice 2: 2026-10-18; selection by code: 2026-10-21)
- **Related:** ADR-113 (canonical signed release), ADR-129 (deploy version gauge), ADR-580 (control-plane convergence in CD), ADR-1736 (zero-downtime restarts), ADR 202610171900 (release compatibility gates), ADR 20261018084150 (expand-only migrations in per-unit releases), ADR 20261018133100 (host-configuration lane), ADR 20261020160744 (daemon blast radius)

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
Since 2026-10-21 the closure picks the candidates, and a host restarts only
those whose compiled code it does not run yet ("Selection by code" below).

A range is eligible when at least one control-plane daemon changed and none of
these did since the base: migrations (since ADR 20261018084150, only those
that are not lane-eligible), `deploy/` (units, roles, rules,
manifests; since ADR 20261018133100, only paths no control-plane host role
reads, and the release's own unit files), go.mod/go.sum, the files
cd-controlplane copies into its bundle, and the release tarball's own
contents. Paths only compute roles read wait instead of blocking. Blocking go.mod also means a unit release never changes a
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
   failure the workflow runs `deployctl rollback --only <services>
   --expect-current <overlay> --to <base>` and the units are back on the
   release the plan was built over. All of this runs under the same public-path
   availability observer as a full activation.

The unit lane never runs role convergence, KGV rotation or fleet-seal
steps. Those inputs are blocking, so they are unchanged since the base,
which already ran them. Migrations follow ADR 20261018084150. When every
migration since the base is expand-only, the control-plane lane applies
them with the release commit's `bin/migrate`, through deployctl's own
migration step, before it restarts anything. Any other migration still
blocks the lane. This is the split between release activation
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
  runs nightly and, since ADR 20261018104452, gates no full tag either. This
  is a deliberate trade the owner made.
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
release that was active before them, and fails the run. A deploy plan passes
that release (`--to`, its `expect_current`). A converge plan reruns the
release already active, so deployctl takes the release its activation history
records before it (see "Rollback target" below). A converge run (a
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
ADR-1736's two-generation hand-off (#4898), it reloads a gateway when both the
running and the new binary support the hand-off and nothing the unit read at
start has changed, and restarts it otherwise. The lanes call `deployctl
deploy --only` and inherit that choice.

**Order and skew.**

- *N±1:* every lane refuses a unit release that is not built over the newest
  full release tag before its commit. That is the release CI compared each
  merge against, so mixed units are at most one release apart.
- *Order:* lane-eligible migrations (ADR 20261018084150) are applied by the
  control-plane lane before its restarts, and contract migrations go out
  only in a full release. `release-units.yml` deploys the control-plane
  lane, then the edge lane. When the edge lane runs alone, it checks the
  control plane itself, including the applied migrations. Runtime (guest,
  images) waits for a full release.

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

## First production run (production-us, 2026-10-20)

`cd-controlplane` run 39473046675 deployed unit release 39471750816
(rc.283 to 5bb86b8ecd: eight control-plane daemons, three expand migrations).
Staging, migrations, activation and readiness passed. The SLO gate then
rolled every unit back to rc.283, and rollback worked as designed. None of
the three breaches came from the new code:

- `apid`: `FaasTrafficSpikeAccount` fired on an end-to-end test's traffic.
  Families that measure tenant behaviour (`traffic_anomaly`, `tenant_abuse`,
  `security_anomaly`, `residency`) no longer gate a unit; platform
  availability and latency SLOs still judge every unit.
- `meterd`: `FaasResidentGbPerCustomerHigh` was true before the deploy. It
  resolved while meterd restarted, because the gauge disappears, then fired
  again with a new `activeAt`. An instance seen true before the deploy now
  stays ignored for the rest of the gate.
- `s3-gatewayd`: production-us runs it without a scrape target
  (`faas_object_storage_gateway_managed` is off), so "no healthy scrape
  target" could never clear. A job with no `up` series at all is now skipped,
  and readiness checks cover it. A configured target at `up == 0` still fails.

## Rollback target (production-us, 2026-10-20)

Run 39473046675 activated overlay c7fe89a5 over rc.283 (ec2334def4) and its
gate rolled it back to rc.283. Run 39480533579 then activated overlay
21b559ca over rc.283, its gate failed, and `deployctl rollback --only`
restored c7fe89a5: the overlay run 1 had already rolled back. The rollback
took the newest retained directory other than the active one. Directory
times say when a release was installed, not which release a deploy
replaced, so automatic rollback was unsafe from the second failed run on.

A rollback now restores exactly the release that was active before the
deploy it undoes:

- `deployctl deploy` (full and `--only` alike) records each activation in
  `<releases root>/.activation-history.json`, a bounded stack of release ids
  written before the activation and unwound when the deploy rolls itself
  back. `deployctl rollback` pops it.
- `deployctl rollback --to <release-id>` names the target instead, and
  `--expect-current <release-id>` refuses unless that release is still
  active, so a failed gate never rolls back a release deployed since. When
  the target is already active the rollback is a converged rerun: it only
  restarts daemons still on another release.
- Without `--to` and without a history entry for the active release (it was
  activated by an older deployctl, or the history is already unwound to
  it), `deployctl rollback` refuses and lists the retained releases. It no
  longer guesses.
- Retention keeps the active release's rollback target even when newer,
  rolled-back releases fill the retention window.

The unit lanes pass `--expect-current <overlay> --to <base>` from the plan,
and `--expect-current` alone for a converge plan. cd-platform's full-release
rollback runs `deployctl rollback` without flags, and the release it just
activated has recorded its predecessor.

## Selection by code (2026-10-21)

**Context.** A unit release carried and restarted every daemon whose import
closure changed since the base. ADR 20261020160744 measured that this is a
coarse proxy: of the 12, 12 and 18 release binaries whose closures #5045,
#5048 and #5052 touched, the compiled code of 1, 1 and 6 changed (with
`grpcnotrace`). Two effects compound it:

- Comparing release binaries' bytes does not help: every Go binary embeds
  a build ID that hashes its whole closure, and the unit version stamp
  differs per commit.
- Unit versions are cumulative over the base. Once a daemon's closure
  changed, every later merge that touched it again moved its unit version
  and restarted it, with the same code. A change to `pkg/state`, which
  almost every daemon links, therefore restarted almost every control-plane
  and edge daemon on each merge.

**Decision.**

1. *Candidates stay the closure.* `affected_units.py` still computes each
   binary's closure and unit version as above. A lane is eligible, and its
   tarball carries a binary, exactly when it was before.
2. *Measure the candidates' code.* With `--code` (release-units.yml uses it),
   every candidate of a deployable lane that nothing blocks is built twice:
   at the base, from an exported tree with the base's own recipe, and at the
   head, from the checkout. Both builds use the release flags
   (linux/amd64, CGO off, `GO_BUILD_FLAGS`, `DAEMON_TAGS` for daemons,
   `-s -w`), the neutral version stamp `code` and an empty build ID
   (`scripts/ci/binary_code.py`, shared with `blast_radius.py`). The code
   digest is a sha256 over the name, size and contents of every section
   the program loads, except `.gopclntab` (line, file and function tables)
   and `.go.module` (the runtime's module data), and over no addresses. A
   line table that grows moves the sections after it and changes the
   module data's pointers to them; code or data that refers to a moved
   section changes its own bytes, which the digest covers. On main at
   #5070, a comment added at the top of `pkg/state/types.go` grows apid's
   line table by 16 bytes and moves `.typelink` and `.itablink`: digesting
   addresses would have called that a code change. A candidate is
   `changed` (or `added`) when the code digests differ, `same` when the
   files are identical, and `lines` when only the excluded sections
   differ. The manifest records, for every unit it carries, the status,
   the head code digest and the base code digest. A blocked lane is not
   built: it ships nothing, and a full release ships every binary. The
   shared, compute and tool lanes are reported by closure, as before. If
   a code build fails, the release falls back to the closure: it carries
   no digests, and hosts restart by version, which restarts more units,
   never fewer.
3. *The host decides what restarts.* `plan_unit_deploy.py` restarts a unit
   when the code the host runs differs from the release's: the digest
   `unit-versions.json` records for it (`unit_code`), or the base code
   digest when the host runs the base binary. A unit whose code the host
   already runs keeps its binary and its recorded version. When either side
   has no digest (a release or a record from before this change), the unit
   versions decide, as before. The edge lane's deploy-order check plans the
   control-plane lane with the same rule, so it accepts a control plane
   that runs the release's code under an older unit version.
4. *Unchanged units still ride along.* The tarball keeps carrying the
   `same` and `lines` candidates. A revert is the reason. A fix to apid is
   deployed by one unit release, found bad after its gate, and reverted:
   apid's code at the head now equals the base's again. A host that runs
   the base never restarts it, because the release's code digest equals the
   base code digest. The host that runs the bad apid does restart it,
   because its recorded digest differs, and it needs a binary for that.
   Shipping only `changed` units would leave that host with no binary to
   restore and an eligibility rule that cannot see the revert. Carrying a
   unit costs its link and its share of the tarball, not a restart.
5. *Unit versions.* The stamp shipped binaries carry is unchanged:
   `unit-<newest closure commit>`, the source the binary is built from (the
   inputs are identical from that commit to the head, so the bytes are
   reproducible from it). Only the comparison uses the neutral stamp, so the
   per-commit stamp no longer makes every carried binary look new. Because
   a host keeps the version of a unit whose code it already runs, its
   `unit-versions.json` names the commit each running binary was built
   from, and two hosts, or a host and the newest release, can name
   different versions for the same code. The skew checks still hold:
   `FaasDeployVersionSkew` ignores `unit-*` versions; the plan compares
   digests and falls back to versions; the overlay id is still derived
   from the recorded versions and the release commit, so a rerun assembles
   the same directory.
6. *Line-number-only differences ride along; they do not restart.* A
   `lines` binary runs the same instructions; only the positions in stack
   traces and `slog` source attributes moved, because an edit above the
   functions it links shifted their line numbers. Not restarting keeps
   those positions accurate: the running binary was built from the commit
   its version names, so a trace maps exactly onto that commit's source. A
   restart would only make the positions match a newer commit with the same
   code, at the cost of the restart (for vmmd, once it joins a lane, a
   compute drain). #5052 had four such binaries, vmmd among them. The next
   release that changes a binary's code ships its current line table, and
   every full release ships every binary.

   One blind spot is accepted. In Go 1.26, `.gopclntab` also holds each
   function's metadata: GC stack maps, inline trees, defer records. A
   change that alters only that metadata, with identical instructions,
   data and type descriptors, is classified `lines`. That takes a word
   reinterpreted between `uintptr` and a pointer type and nothing else
   (two non-test files use `unsafe.Pointer`). Telling it apart from a line
   shift means decoding the table (an inlined function's start line lives
   in the same metadata), which is version-specific parsing this change
   does not take on. Such a change reaches production with the next code
   change to the binary or the next full release.
7. *Build cost.* Only the candidates of unblocked deployable lanes are
   built, and the compile work is shared: the head's neutral build and the
   release build differ only in their link flags, and identical packages at
   base and head hit the same build-cache entries (`-trimpath` keeps the
   directory out of the action ID). The base's builds are the same for
   every unit release over one base, so their digests are cached in the
   Actions cache per base commit (`--code-cache`; an entry names its base,
   neutral stamp and Go toolchain and is ignored otherwise). Only the first
   unit release over a new base builds the base. On the hosted runner,
   today's head build takes 5-6 minutes (runs 39486341829-39493516693), so
   that first run takes about 5 minutes longer and later runs about as long
   as before plus one extra link per carried binary. go.mod and go.sum
   block the lane, so the base and head builds always use the base's
   toolchain.
8. *Unchanged.* N±1 (the base and previous-release checks), the signed
   tarball and its manifest (the code fields are additive and covered by
   the signature; the format stays 2), each binary's sha256 and size,
   deployctl, the data step and the host-configuration step.

**Simulation.** The last 30 merges to main (#5034 to #5070), each taken
alone (parent to merge), with the release flags of today (`grpcnotrace`
added to the 21 merges that predate #5059). "Closure" is what a release
restarted before this change; "code" what restarts now on a host that runs
the parent; "lines" binaries ride along without restarting.

| merge | closure: all / lanes | code: all / lanes | lines | lane binaries whose code changed |
| --- | --- | --- | --- | --- |
| #5070 | 1 / 0 | 1 / 0 | 0 | - |
| #5065 | 27 / 10 | 27 / 10 | 0 | apid, bridged, gatewayd-internal, gatewayd-public, githubd, livequeryd, maild, meterd, outboundd, s3-gatewayd |
| #5060 | 2 / 1 | 2 / 1 | 0 | apid |
| #5066 | 9 / 6 | 2 / 1 | 7 | outboundd |
| #5068 | 0 / 0 | 0 / 0 | 0 | - |
| #5041 | 27 / 10 | 0 / 0 | 1 | - |
| #5019 | 27 / 10 | 15 / 8 | 0 | apid, bridged, gatewayd-internal, gatewayd-public, githubd, meterd, outboundd, s3-gatewayd |
| #5063 | 0 / 0 | 0 / 0 | 0 | - |
| #5059 | 28 / 10 | 26 / 10 | 0 | apid, bridged, gatewayd-internal, gatewayd-public, githubd, livequeryd, maild, meterd, outboundd, s3-gatewayd |
| #5062 | 18 / 10 | 5 / 3 | 5 | apid, gatewayd-internal, meterd |
| #5061 | 0 / 0 | 0 / 0 | 0 | - |
| #5058 | 10 / 6 | 10 / 6 | 0 | apid, bridged, gatewayd-internal, gatewayd-public, meterd, outboundd |
| #5057 | 18 / 10 | 5 / 1 | 9 | apid |
| #5050 | 27 / 10 | 14 / 8 | 13 | apid, bridged, gatewayd-internal, gatewayd-public, githubd, meterd, outboundd, s3-gatewayd |
| #5056 | 0 / 0 | 0 / 0 | 0 | - |
| #5037 | 27 / 10 | 18 / 10 | 9 | apid, bridged, gatewayd-internal, gatewayd-public, githubd, livequeryd, maild, meterd, outboundd, s3-gatewayd |
| #5055 | 12 / 6 | 1 / 1 | 11 | outboundd |
| #5054 | 2 / 1 | 1 / 1 | 0 | apid |
| #5051 | 27 / 10 | 2 / 1 | 25 | apid |
| #5052 | 18 / 10 | 6 / 2 | 4 | apid, meterd |
| #5053 | 0 / 0 | 0 / 0 | 0 | - |
| #5046 | 3 / 1 | 2 / 1 | 0 | apid |
| #5036 | 27 / 10 | 17 / 9 | 10 | apid, bridged, gatewayd-internal, gatewayd-public, githubd, maild, meterd, outboundd, s3-gatewayd |
| #5044 | 27 / 10 | 17 / 7 | 10 | apid, bridged, gatewayd-internal, gatewayd-public, meterd, outboundd, s3-gatewayd |
| #5048 | 12 / 6 | 1 / 0 | 0 | - |
| #5043 | 2 / 1 | 1 / 1 | 0 | apid |
| #5035 | 27 / 10 | 18 / 10 | 9 | apid, bridged, gatewayd-internal, gatewayd-public, githubd, livequeryd, maild, meterd, outboundd, s3-gatewayd |
| #5045 | 12 / 6 | 1 / 0 | 0 | - |
| #5042 | 1 / 1 | 1 / 1 | 0 | livequeryd |
| #5034 | 27 / 10 | 21 / 10 | 6 | apid, bridged, gatewayd-internal, gatewayd-public, githubd, livequeryd, maild, meterd, outboundd, s3-gatewayd |
| **total** | **418 / 175** | **214 / 102** | 119 | |

Release binaries restarted, closure → code: 418 → 214 in all (median per
merge 12 → 2), 175 → 102 in the control-plane and edge lanes (median 6 →
1). For the 12 fixes: 52 → 18 in the lanes (median 3.5 → 1); #5045, #5048
and #5052 come out at 1 (schedd), 1 (vmmd) and 6 binaries, as ADR
20261020160744 measured. vmmd's code changed in 14 of the 19 merges that
touched its closure. Merges that change types in shared packages
(`pkg/state`, `pkg/api`: #5065, #5019, #5035 to #5037) still change most
daemons' code, because type descriptors and method sets are linked into
every binary that uses the package. 119 binaries (38 in the lanes) differed
only in their line tables; restarting those would have taken the lanes
from 102 to 140. On an Apple-silicon laptop, building a merge's
candidates at its parent and at the merge took a median of 78 and 10
seconds with a cold cache for each parent, and 12 and 10 seconds with
every package cached.

**Consequences.**

- A fix restarts the daemons whose code it changes, on the hosts that do
  not run that code yet, and nothing else; later merges that touch the same
  closures without changing their code restart nothing.
- A unit release that restarts nothing on a host whose active overlay
  replaced units of the lane still plans `converge`, which re-runs that
  overlay's activation and gate (unchanged behaviour). Skipping the gate
  once a record says it passed is a possible follow-up.
- Changing the neutral stamp, the recipe or the toolchain changes every
  code digest, so the next unit release restarts every unit it carries,
  once. That is the safe direction.
- A deploy job run from a checkout older than this change ignores the
  digests and restarts by version, as before.
- Follow-up: compute the base digests when a full release is tagged (a
  `workflow_run` job in the default branch's cache scope), so even the first
  unit release over a base skips the base build.

## Still needed

- **Migrations.** Done in ADR 20261018084150. Every new migration declares
  `-- migration-compat: expand|contract`. The control-plane lane applies a
  range of expand migrations ahead of restarts, and contract migrations
  still need a full release. After that change, `deploy/` changes are what
  most often close the lane.
- **Compute daemons beyond the gateways.** schedd and realtimed restart
  without touching VMs and could join the edge lane once their restarts are
  covered (ADR-1736). vmmd needs the restart-adoption work.
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
- **Full lane, outside this ADR.** Host configuration of the control
  plane now rides the control-plane lane, keyed by a per-role content hash
  (ADR 20261018133100); compute host roles are designed there and still
  wait for the full release. Content-address runtime bases and pre-stage
  them off the critical path. Roll compute as a canary node, then parallel
  waves, once activation is per-unit without a drain.
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
- **Carry only the units whose code changed (selection by code).** After a
  revert to base code, a host that runs the reverted unit would have no
  binary to restore, and the lane's eligibility could not see the revert.
- **Unit versions from the commit that changed the code.** Finding that
  commit means building the binary at every closure commit since the base.
  The digest the host records answers the same question exactly, at deploy
  time.
- **Compare code by a digest over section addresses too.** A one-line
  comment that grows the line table moves the sections after it, and would
  count as a code change (measured on apid).
