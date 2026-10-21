# ADR-1500: Source-built apps run on the shared runtime base

Date: 2026-10-17
Status: Accepted (owner chose option A for H10-140; rollout gated by a node flag)
Related: spec §4.6 (two-drive rootfs), ADR-595 (application standards; its source-rootfs intent SQL derives the layer kind from `app.runtime` and needs a migration for slice 2), H10-139, H10-140

Historical filename: merged migration comments retain `20261017-shared-runtime-source-apps.md`; this decision is now ADR-1500.

## Context

Spec §4.6 puts the shared read-only runtime on drive0 and only the per-app
layer on drive1; flattening "duplicates ~150 MB of base per app and breaks the
130 MB/sandbox disk economics". Functions follow it: imaged copies `/app` out of
the build export onto the runner base for their runtime (`runner-node24` and
friends), so Node lives once per node on drive0.

Source-built container apps do not. An app with an empty runtime resolves to
the minimal busybox base, Railpack installs its own toolchain with mise, and
imaged applies every layer of the BuildKit OCI export to drive1. On
production-us (rc.272) a two-file Node worker with no dependencies produced a
404 MiB app layer. Its SBOM shows busybox, `/mise/installs/node/22.23.3`, and
guest-init (`/sbin/init`, 26 MB after H10-139). Every Node app from a working
tree observed since 10-15 is about 420 MB. Consequences:

- The padded layer (at least 442 MB) exceeds the Free plan's 256 MB
  `AppLayerMaxMB`, so a Free-plan Node app from source is refused.
- The layer is about three times the 130 MB fleet snapshot target.
- Deploys take about 8 minutes. The build downloads Node, and imaged ships
  about 400 MB to the peer and scans it.

## Decision

A source build of a container app runs on the shared runner base for its
language when the build is **shared-runtime eligible**. Otherwise it keeps
today's full-layer path unchanged.

1. **Runtime selection (builderd).** For a Railpack build (no Dockerfile) of a
   container app with an empty `app.Runtime` and a Node framework, choose
   `node24`, or `node22` when `.nvmrc` or `engines.node` names major 22. Pass
   it as the build manifest runtime and its pinned base as `RuntimeBaseRef`.
   *Amended 2026-10-17 (acceptance):* the choice reads the declaration as
   written (`sharedruntime.NodeRuntimeForSpec`), because the parsed source
   version drops a bare major: `"engines": {"node": "22"}` used to pick
   `node24`. `22`, `22.x`, `^22`, `~22` and `lts/jod` pick `node22`. No
   declaration, `24`, `lts/krypton`, `lts/*` and a lower bound the 24 base
   satisfies (`>=20`) pick `node24`. Any other declaration (`20`, `^20`, `26`,
   a compound range, `latest`) keeps today's full-layer path: the builder
   pins Railpack's Node to the base's major, so the shared path must never
   replace a declared major.
   This is the same handoff functions use: Railpack builds FROM that base and
   pins its Node package to the base's major. The runtime is recorded on the
   deployment's build plan (`build_plan.runtime`), so the choice is frozen per
   deployment and is part of the build cache key.
2. **Eligibility (builder VM).** After `railpack prepare`, the guest checks
   the plan. A build is eligible only if:
   - deploy inputs are the base image, the language toolchain step and the
     `/app` tree;
   - `aptPackages` is empty and there are no extra deploy paths;
   - the start command is a direct command or `npm run <script>` without
     `pre`/`post` hooks.

   The verdict and the resolved start command go into `build-done.json`. An
   ineligible build reports why and stays on the full-layer path.
   Railpack's Node plan adds a `libatomic1` apt step by default, so the
   platform overlay clears `aptPackages` on runner bases too (they ship
   libatomic), as it already does for the minimal and Alpine bases. A
   customer's explicit list still disqualifies the build.
3. **Start command.** Runner bases ship no npm. An eligible
   `npm run <script>` is resolved at build time to
   `sh -c '<script body>'` with `/app/node_modules/.bin` on `PATH`, which is
   what npm executes. `PATH` drops `/mise` entries and uses the base's Node.
4. **Layer (imaged).** For an eligible deployment, drive1 holds the export's
   `/app` tree plus `/etc/faas/app.json`, the same shape as a function layer
   without the runner binary. The signed export claim names the runtime, and
   the `source-app-layer` record carries it, so ADR-595's base-producer
   validation uses the runner base (the owner chose this narrow variant over a
   new rootfs kind). drive0 is not taken from the record: schedd picks it
   (`artifactBaseKey`: a runtime release, else the app's runtime base). The
   deployment therefore records the runner base in `deployments.runtime_base`,
   stamped in the same transaction as the record, and schedd's wake, prime and
   app-task boots use it. Job-mode boots and environment qualification still
   assume the minimal base, so they stay ineligible until they read it.
5. **Rollout.** Nodes enable the path with
   `FAAS_SOURCE_APP_SHARED_RUNTIME=1`; the default is off until a
   native-metal acceptance run (cold boot, snapshot restore, rollback across
   the two layer kinds) passes. Existing deployments keep their layers and
   bases. A redeploy moves an app over, and a rollback restores the older
   deployment with its own base.

Python follows the same model (`runner-python312/313`, `.venv` under `/app`
with `PYTHONPATH`, as functions use) in a later slice.

## Slices

1. `pkg/sharedruntime` classifier; guest-init records the verdict in
   `build-done.json` and builderd logs it. No behavior change; it measures
   eligibility on real builds.
2. `source-app-layer` records may carry a shared runner base named by the
   signed claim (Go validation; migration of the CHECK, the publication lock
   and the runtime-producer SQL).
3. a. `deployments.runtime_base`, stamped with the record; schedd boots it;
      environment clones carry it.
   b. builderd runtime selection and claim, guest-init `aptPackages` cleared,
      imaged `/app` layer assembly, behind `FAAS_SOURCE_APP_SHARED_RUNTIME`,
      default off; job mode excluded.
4. Native-metal acceptance, then default on. Python.

## Consequences

- An eligible Node app layer is its source plus `node_modules`, a few MB for
  a typical small app, instead of about 420 MB. That fits every plan's cap
  and the 130 MB target. Replication, scan and snapshot drive copies shrink
  to match.
- Builds still install Node with mise inside the builder VM, because Railpack
  needs a toolchain for `npm ci`. Only the runtime artifact drops it.
  Reusing the base's Node in the build is a later optimization.
- Native addons are built against the Railpack Node of the same major as the
  base, so ABI compatibility holds within the major.
- Runtime upgrades (ADR runtime-upgrade machinery) remain function-only. A
  shared-runtime app moves to a new base by redeploying.
- Apps that install system packages, write outside `/app` at build time, or
  use npm lifecycle hooks keep the full-layer path and pay today's size.

## Alternatives

- **Raise the Free layer cap.** This contradicts the financial model and
  leaves the duplication.
- **Document Free as functions-only.** This hides the cost instead of
  removing it.
- **Flatten the base into drive1 and dedupe on disk.** Reflink dedupe
  (ADR-633) cannot dedupe across different files, and §4.6 forbids
  flattening.
