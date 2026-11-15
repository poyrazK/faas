# ADR-1170 · Developer watch mode for apps with a build step

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
     `NODE_ENV=development`; guest-init bridges the runtime port to the
     development server (phase 3).
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
framework's reload follows within its own compile time. A watch-mode patch
also rewrites an existing file in place rather than renaming a staged copy
over it (amendment 1).

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
   requirement; metrics `watch` label; metal tests. *Implemented; the
   metrics label followed on 2026-10-14 (every apid dev-loop series carries
   `watch="true|false"` and the dashboard splits edit-to-live, decisions and
   phases on it). `TestDevWatchMetal` passed on the native KVM test node
   (2026-10-11): a Node app with a build script built as a verbatim watch
   build, served `npm run dev`, and an edit reached the running development
   server as a live patch (applied in 14 ms) that `node --watch` reloaded
   without a guest-init restart; `TestDevLoopMetal` passed in the same run
   and leakcheck stayed clean.*
   - `gregale dev --watch` / `--watch-command` and `dev.watch` (`true` or
     `{command}`). Without a command the CLI uses `npm run dev` when
     `package.json` has a `dev` script, which pulls part of phase 3 forward.
   - apid validates the command and refuses watch mode with
     `dev_watch_unsupported` for functions, developer apps under
     `DevWatchMinRAMMB` (512 MB) and apps reachable without authentication.
     The setting lives in `dev_watch_settings`; an upsert without `watch`
     clears it.
   - builderd passes the command to developer builds only (production apps,
     PR previews and functions never get it) and keys the build cache on it.
   - The rewrite is a Railpack config overlay, not a plan rewrite: guest-init
     replaces the `build` step's commands with a non-executing `path`
     command, sets the development command as `deploy.startCommand`, and adds
     `NODE_ENV=development` and `FAAS_DEV_WATCH=1` to the deploy variables.
     The plan Railpack then writes must classify as verbatim, or the build
     fails with `dev_watch_unsupported`; Dockerfile and non-Node builds are
     refused before they start.
   - In an image with `FAAS_DEV_WATCH=1`, guest-init writes live patches and
     acknowledges them without restarting the workload.
3. **Framework defaults.** `dev.watch: true` resolves the command from the
   package's `dev` script or known frameworks (Next.js, Vite, Nuxt, Remix,
   SvelteKit). The rebuild explainer suggests `--watch`. *Implemented
   (2026-10-13):*
   - `frameworkprofile.DevServerCommand` picks the command: the `dev` script
     through the package manager Railpack detects for the upload
     (`packageManager` field, else the lockfile: npm, pnpm, yarn or bun;
     Railpack starts production apps with the same manager, so the runtime
     image has it), else the detected framework's own command (`nuxt dev`,
     `astro dev`, `vite dev`, `react-router dev`, `remix vite:dev`,
     `next dev`, `vite`). Anything else still needs a `dev` script or
     `--watch-command`.
   - The host reaches a guest on the deployment's runtime port (inferred,
     e.g. 3000 for Node) at 10.0.0.2, but development servers listen on
     their own port (Vite 5173, Astro 4321) and often only on localhost; the
     first cut appended `--host 0.0.0.0`, which fixed neither the port nor a
     composite `dev` script. Instead, in an image with `FAAS_DEV_WATCH=1`,
     guest-init waits for the server's listeners to settle and, unless one
     already listens on the runtime port on every interface, forwards
     `10.0.0.2:<runtime port>` to the server's socket (loopback, IPv6 or
     another port). Production images keep the listen-on-`$PORT` contract
     and `app_loopback_bound`. Characterization now decodes `tcp6`
     addresses and brackets them; before, every IPv6 listener was reported
     as an unparsable `::1:<port>`, so a `[::1]` bind never produced
     `app_loopback_bound`.
   - Development servers also refuse requests for hosts or origins they do
     not know: Vite 5.4.12+/6.0.9+ answers 403 to an unknown `Host`, and
     Next.js 16 blocks the HMR websocket from an origin outside
     `allowedDevOrigins`. The gateway forwards the customer `Host`, so apid
     stores the developer app's public hostname with the watch command
     (`dev_watch_settings.host`), builderd passes it in the build manifest
     and keys the build cache on it, and the watch overlay sets
     `GREGALE_DEV_HOST` in the image. guest-init adds Vite's
     `__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS` from it when a watch-mode
     workload starts: image environment keys must start with a letter, so
     the image cannot carry Vite's name (the first metal run caught the
     dropped variable as a 403). Next.js reads `allowedDevOrigins` only from its config
     file, so with a terminal `gregale dev --watch` offers to add
     `process.env.GREGALE_DEV_HOST` there (creating `next.config.mjs`, or
     editing a config that exports a plain object) and otherwise prints the
     line. Django's `ALLOWED_HOSTS` gets a printed reminder.
   - `gregale dev --all --watch` passes `--watch` to every member with a
     command to run; the others keep production-shaped builds, and a
     member's own `dev.watch.command` still applies.
   - Metal (internal test node, 2026-10-14, b85a11d5c9): `TestDevWatchMetal`
     passed with a fixture that listens like Vite's default
     (`127.0.0.1:5173`, refusing unknown hosts) and plain `npm run dev`.
     guest-init logged `bridging the runtime port to the development
     server port=3000 target=127.0.0.1:5173`; the environment's hostname
     got 200 and `GREGALE_DEV_HOST`; an edit hot-reloaded in 7 s without a
     restart; the second build kept the dependency cache. No new leaks
     (the host's pre-existing leakcheck entries were unchanged).
   - The rebuild explainer suggests `--watch`, with the command it would
     run, only when the CLI can pick one for the app.
   - Checked against the real servers (2026-10-13, local): Vite 8.3.4's
     `npm run dev` listens on `[::1]` only; `npm run dev -- --host 0.0.0.0`
     listens on every interface but answers 403 to the app hostname, and
     with `__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS` set it serves the page
     (200) and accepts the HMR websocket (101; 400 without it). Next.js
     16.4.0's `next dev` listens on every interface and serves the page for
     the app hostname, but drops the HMR websocket (`/_next/hmr`) from that
     origin unless `allowedDevOrigins` lists it;
     `allowedDevOrigins: [process.env.GREGALE_DEV_HOST]` opens it (101).
4. **Python.** `uvicorn --reload` / Flask debug for uv projects, whose
   `uv sync` over the full source makes them non-verbatim today.
   *Implemented (2026-10-14):* the overlay accepts Railpack Python plans,
   replaces the build step's commands with `{"path": "/app/.venv/bin"}`
   (dropping the `uv sync` that installs the project over the full source)
   and sets `PYTHONPATH=/app/src:/app` so a `src/` layout still imports
   itself. The pinned uv plan then classifies verbatim. Default commands:
   `python manage.py runserver` (Django), `uvicorn <module>:<app> --reload`
   (FastAPI) and `flask --app <module>:<app> run --debug` (Flask), found the
   way production inference finds the app object; all three listen on
   localhost and reach the host through the bridge.

## Amendment 1 (2026-10-26): watch-mode patches write files in place

ADR-740 stages each patched file beside its target and renames it into place.
That suits the restart path: the restarted process reads the new inode, and a
half-written file is never visible. It breaks watch mode. `node --watch`
watches the inode it first saw, so after one renamed patch it never reports a
later edit to the same file. Measured on the test node with Node 22.23.3 (the
version the builder installs): three renames over `index.js` gave one restart
(v1 to v2 only); three in-place writes gave three (v2, v3, v4). The metal test
`TestDevWatchMetal/saves-coalesce-into-one-build` hit it: the second patch was
acknowledged as applied, and the server kept serving the first patch's body.

Decision: in watch mode guest-init applies patches with
`devpatch.ApplyInPlace`. It reads each entry fully, then truncates and
rewrites an existing regular file through its own inode and restores the
masked mode. New files, symlinks and files with more than one hard link still
take the staged rename, so a write never follows a link and never changes a
second path. `os.Root` confinement is unchanged. Apps not in watch mode keep
ADR-740's staged rename.

Consequence: a write that fails part way (for example a full disk) can leave
that one file truncated, where the staged rename left it untouched. The
generation is acknowledged as `apply_failed`, and the next sync republishes
the whole cumulative patch. Editors that save in place (VS Code's default)
already expose development servers to the same window.
