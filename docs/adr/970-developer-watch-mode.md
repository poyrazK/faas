# ADR-970 · Developer watch mode for apps with a build step

- **Status:** proposed
- **Date:** 2026-10-10
- **Decision:** A `gregale dev` environment can opt into **watch mode**. Its
  builder produces a developer image from the same Railpack plan with the
  build step removed from the image, development dependencies kept, and the
  start command replaced by the framework's own development command
  (`next dev`, `vite`, `tsx watch`, ...). Because that image ships the source
  unchanged, ADR-740 live patches apply to it as they do to a verbatim app,
  and the framework's own watcher recompiles and hot-reloads the edit. Watch
  mode never changes production, PR previews or named environments.
- **Why:** ADR-740 only patches apps whose build copies source unchanged. Every
  app with a build step (TypeScript, Next.js, Vite, Nuxt, Remix, SvelteKit)
  is `build_command` and waits for a full build on every save. Those
  frameworks already ship a fast incremental compiler and reloader for this
  exact loop. The platform only has to run it and get edits to it.

## Context

ADR-740 delivers bounded, cumulative source patches over the runtime-config
vsock to a developer instance, restarts the workload, and keeps the normal
developer build running so the instance converges to a built artifact. It is
deliberately limited to source the build copied verbatim. The builder decides
that from the plan `railpack prepare` writes inside the builder VM
(`pkg/devpatch.ClassifyRailpackPlan`), so eligibility never trusts host-side
inference.

The dev-loop dashboard (`deploy/grafana/dev-loop.json`) reports why syncs
rebuild (`apid_dev_patch_plans_total{outcome="ineligible",reason}`). Before
this work, `build_command` is expected to dominate for JavaScript apps. The
first rollout phase below must confirm that before it widens.

Constraints carried over:

- **Builds run only in builder microVMs (ADR-003).** Watch mode does not move
  `npm install` or `next build` into the tenant VM. Dependency installation
  still happens in the builder. What runs in the developer VM is the
  developer's own development command: customer code in the customer's VM,
  under the plan's RAM and CPU caps, like any other workload.
- **Snapshots are cache (ADR-005).** A patched instance is still never
  snapshotted, and the developer build still converges.
- **Customer-visible parity (ADR-157).** A developer environment that runs a
  development server is not running the production build. That has to be
  explicit, not silent.

## Decision

### Opting in

`gregale.yaml` gains `dev.watch`:

```yaml
dev:
  watch: true            # use the package's "dev" script
  # or
  watch:
    command: "next dev"  # explicit development command
```

`gregale dev --watch` sets the same thing for one session. The CLI sends
`watch` (and an optional command) with the developer session. apid stores it
on the developer app and passes it to the build as part of the build manifest.
Production deploys ignore the setting.

`--watch` is rejected when:

- the plan's RAM for the developer app is below `DevWatchMinRAMMB` (new,
  per plan in `pkg/api/limits.go`); development servers for these frameworks
  need noticeably more memory than the production server;
- the app is built from a Dockerfile (`not_railpack`); a Dockerfile owner
  writes the development stage themselves;
- the developer app does not require authentication. Development servers
  serve source maps, error overlays with source excerpts and HMR endpoints, so
  watch mode is only available behind the developer app's existing access
  check.

### The developer image (builder VM)

1. `railpack prepare` writes the plan exactly as today.
2. guest-init in the builder VM rewrites the plan for watch mode before
   `buildctl` runs:
   - drop the step that runs the build command over the source;
   - keep the install step and stop pruning development dependencies;
   - make the deploy image take the full local source unchanged, so the
     plan is verbatim by `ClassifyRailpackPlan`;
   - replace the start command with the development command, with
     `NODE_ENV=development` and a host/port binding the existing port
     normalization already forwards.
3. The rewritten plan is what gets classified and recorded in
   `build_provenance.dev_patch`, with a new `watch: true` field. The install
   inputs (manifests, lockfiles) become `RebuildPaths` as they are today.
4. If the plan shape is not one the rewrite understands, the build fails with
   `watch_unsupported` and a pointer to `dev.watch.command`. Watch mode never
   silently falls back to a production build.

### Delivering edits

Delivery is ADR-740 unchanged: cumulative patches, the runtime-config poll,
`os.Root`-confined writes under `/app`, the diverged-instance snapshot guard.
One difference: for a `watch: true` build, guest-init does **not** restart
the workload after applying a patch. The development server's own file watcher
picks up the change. guest-init acknowledges the generation once the files are
written. The CLI's `patch` phase then measures save-to-written, and the
framework's reload follows within its own compile time.

A change to a rebuild input (dependencies, lockfile, `railpack.json`,
`gregale.yaml`) still goes through the developer build, which reinstalls in
the builder VM. That is the only case that waits for a build.

### Telling the user

- The ADR-740 explainer (`Full rebuild instead of a live patch: your app has a
  build step...`) adds `run gregale dev --watch to hot-reload it instead`
  when the app's framework is one watch mode supports.
- Every watch-mode session prints once that the environment runs the
  development server, not the production build. `gregale deploy` and PR
  previews remain the place a production build is exercised.

## Consequences

- Build-step apps get save-to-reload in roughly patch delivery plus the
  framework's incremental compile, instead of a full build.
- New: `dev.watch` in the manifest and CLI, a watch flag on developer apps
  and build manifests, the builder plan rewrite in guest-init, `watch` in
  `DevPatchSourceMap`, the no-restart apply path, `DevWatchMinRAMMB`, and a
  `watch` label on the dev-loop metrics so the dashboard can compare the two
  paths.
- A developer environment in watch mode can behave differently from
  production: dev-only code paths, unminified output, development React.
  That is the trade every local dev server already makes, and it is stated in
  the CLI rather than hidden.
- Development servers use more memory and CPU than production servers, so
  watch-mode environments are billed for the RAM they need, at the same
  plan RAM + 8 MB rate as every other instance (§4.7).
- Metal tests must prove: the rewritten plan is classified verbatim; an edit
  reaches the running development server without a process restart; a
  dependency change rebuilds; the snapshot guard still refuses patched
  instances; `leakcheck` stays clean.

## Rejected alternatives

- **Keep a warm builder VM per developer session and rebuild incrementally.**
  Respects ADR-003 most literally, but the control plane guarantees one
  builder slot and the second is opportunistic. One held builder per active
  developer is not affordable, and `next build` stays slow even when warm.
- **Run the production build inside the developer VM after each patch.** Same
  slow build on every save, now under the app's RAM cap, and against ADR-740's
  rule that a patch never runs a build step.
- **A platform transpiler in guest-init (esbuild/swc per changed file).**
  Fast for plain TypeScript, wrong for frameworks whose build does far more
  than strip types (routing, server components, CSS modules). It would also
  make the platform responsible for every framework's compile semantics.
- **Turn watch mode on automatically for detected frameworks.** It changes
  what the environment runs. Making it opt-in first, and suggesting it in the
  rebuild explainer, keeps that choice with the developer. Revisit once the
  dashboard shows how often it is adopted and kept.

## Rollout

1. **Measure.** Ship the ADR-740 rollout and dev-loop dashboard
   (`faas_dev_patch_delivery_enabled`). Confirm `build_command` is the top
   rebuild reason and which frameworks it comes from.
2. **Node watch mode, explicit command.** `dev.watch.command` only; builder
   plan rewrite for Railpack Node plans; no-restart apply; RAM floor; auth
   requirement; metrics `watch` label; metal tests.
3. **Framework defaults.** `dev.watch: true` resolves the command from the
   package's `dev` script or known frameworks (Next.js, Vite, Nuxt, Remix,
   SvelteKit). The rebuild explainer suggests `--watch`.
4. **Python.** `uvicorn --reload` / Flask debug for uv projects, whose
   `uv sync` over the full source makes them non-verbatim today.
