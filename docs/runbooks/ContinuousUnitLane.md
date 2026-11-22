# Continuous unit lane: turn it on, read it, turn it off

Decision: [ADR 20261122170000](../adr/20261122170000-continuous-unit-lane.md)
(whole commits, under four minutes, §3a safety). Freeze and preflight:
[DeployFreeze.md](DeployFreeze.md).

Every merge to main builds a unit release of the newest whole-commit prefix
the lane can carry (`release-units.yml`). With automatic deploys on it goes
to production without anyone pressing a button: control plane first, then
the edge, then the 30-minute watch with automatic rollback.

## Turn it on (owner, once, in this order)

1. **Smoke account.** Follow [tests/unit-smoke-app/README.md](../../tests/unit-smoke-app/README.md):
   deploy `unit-smoke` on a dedicated account, create its key, then

   ```bash
   gh secret set UNIT_SMOKE_API_KEY --env production-us   # paste the key
   gh variable set UNIT_SMOKE_APP --body unit-smoke
   ```

   Automatic deploys refuse to start without these (the preflight's
   "Require the critical smoke checks" step fails).

2. **Second fleet runner instance.** The preflight now starts beside the
   build and holds a `faas-fleet-us` runner while it waits; another run's
   lanes need a second one:

   ```bash
   ansible-playbook -i <inventory> deploy/ansible/fleet_runner.yml \
     -e faas_runner_hosts=fleet_runners -e faas_runner_instances=2 \
     -e faas_runner_labels='["faas-fleet-us"]'
   ```

   Check: `gh api repos/poyrazK/faas/actions/runners --jq '.runners[] | select(any(.labels[]; .name=="faas-fleet-us")) | .name+" "+.status'`
   lists two online runners.

3. **Build runner on the control plane** (optional for safety, needed for
   speed: the hosted runner adds about a minute of queue, checkout and cache
   restore):

   ```bash
   FAAS_RUNNER_REGISTRATION_TOKEN=$(gh api -X POST repos/poyrazK/faas/actions/runners/registration-token --jq .token) \
     ansible-playbook -i <inventory> deploy/ansible/build_runner.yml
   gh variable set UNIT_RELEASE_BUILD_RUNNER --body '["self-hosted","linux","faas-build-us"]'
   ```

   Unset the variable to go back to GitHub-hosted builds.

4. **Merge the lane** (PR #7864). The first unit release after it restarts
   every unit it carries once: the code digests changed with the neutral
   version stamp ("One build" in the ADR).

5. **Dry run by hand.** Dispatch one deploy and read its summaries:

   ```bash
   gh workflow run release-units.yml -f deploy_environment=production-us
   ```

   Expect: build summary names the release and its units; preflight **Go.**;
   the control-plane lane activates, the smoke checks pass, the SLO gate
   promotes; the watch starts.

6. **Automatic deploys on.**

   ```bash
   gh variable set UNIT_RELEASE_AUTODEPLOY_ENVIRONMENT --body production-us
   ```

## Turn it off

- Pause: open a `deploy-freeze` issue ([DeployFreeze.md](DeployFreeze.md)).
  Merges keep building; nothing deploys until it is closed.
- Off: `gh variable delete UNIT_RELEASE_AUTODEPLOY_ENVIRONMENT`. Releases
  are still built; deploy them by dispatch or with a full release.
- Parallel restarts off: set `activation.parallel` to `1` in
  `scripts/ci/release_units.json` (one PR); daemons then restart one at a
  time as in a full release.

## Reading a run

| What the build summary says | Meaning | What to do |
| --- | --- | --- |
| releases `<sha>`, behind main's head | a later commit is not unit-deployable (blocking path, cross-lane, stateful restart); the prefix up to it ships | nothing; ship the rest with a full release (`cd-platform`, manual) |
| blast cap: releasing the first 5 | more than 5 commits since production last took a release cleanly | nothing; the 10-minute schedule continues with the rest |
| Test the shipped tree failed | the released tree is not the tree CI tested and its tests failed | fix main; nothing deployed |
| not eligible | nothing deployable since the base full release | nothing |

| Preflight says | Meaning |
| --- | --- |
| built: this run's unit release is building | waiting for the build (polled every 10 s) |
| built: released nothing / build ended failure | stands down green; nothing to deploy |
| newest: a newer run deploys instead | releases are cumulative; the newer run carries this one |
| current: run N already deployed | a stalled lane does not redeploy the same commit |
| idle / healthy / watch waits | see [DeployFreeze.md](DeployFreeze.md) |

After activation:

- **Smoke checks failed** → the units were rolled back on the spot; the run
  fails. The step log names the failing check (account, app, env + restart,
  public URL, usage).
- **SLO gate breached** → rolled back on the spot.
- **Watch breach within 30 minutes** → every host goes back to its known-good
  release, host roles converge back to their known-good commit
  (`restore-host-config`), and a `deploy-freeze` issue opens. Close it once
  the cause is reverted.

## Weekly rollback drill

`unit-rollback-drill.yml` runs every Monday 06:17 UTC and changes nothing.
It checks every fact an automatic rollback needs (`scripts/ci/rollback_drill.py`):
the active release and deployctl's activation history agree, the release
before it and the watch's known-good release are still installed, the
host-config record exists, Prometheus answers, and the `deploy-freeze`
label exists. A failed check fails the run and opens an issue (not a
freeze): the next breach could not be undone automatically, so fix it
before the next deploy.
