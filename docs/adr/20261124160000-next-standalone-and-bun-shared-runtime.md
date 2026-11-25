# ADR-20261124160000: Next.js standalone builds and Bun on the shared runtime

Status: Accepted

Extends ADR-1500 (shared-runtime source apps) and ADR-20261029103512.

## Context

The Free plan caps the app layer, and with it the ephemeral disk, at 256 MB
(`AppLayerMaxMB`). On 2026-11-24 the Next.js and Bun deploy guides were
verified on prod and both needed Hobby:

- **Next.js 16** builds on the shared Node 24 runner base, so its layer is
  `/app` only, but `/app` holds the whole `node_modules`. A
  `create-next-app` app with a Linux install has 467 MB of `node_modules`:
  `next/dist` is 207 MB and `@next/swc-linux-x64-gnu` is 97 MB, and both
  are needed only by `next build`. After `npm prune --omit=dev`
  (Railpack's `RAILPACK_PRUNE_DEPS`), 338 MB are left, which is still too
  large. Next's own answer is `output: "standalone"`: `next build` traces
  the files the server needs into `.next/standalone`, which is 38 MB for
  the same app.
- **Bun** apps start with `bun run start`. The shared runtime refused any
  package-manager start other than npm (`package_manager_start`), so the
  layer kept the whole mise toolchain. Railpack installs Node next to Bun
  for any app with dependencies, so the layer held Bun (88 MB), Node and
  `node_modules`: about 420 MB.

A Free-plan full-rootfs override would not help. A full-rootfs layer is a
superset of the two-drive layer, and it is capped by the same plan limit.
Raising Free's cap would also raise its disk and storage cost for every
Free app.

## Decision

**Next.js standalone.** On a shared-runtime build, guest-init rewrites
Railpack's plan after `prepare` for an app that depends on `next` 16 or
later, builds with `next build`, and starts with a plain `next start`
through its package manager. The build step:

1. writes a build adapter to `/tmp` (outside every deploy input) whose
   `modifyConfig` sets `output: "standalone"` for the production build,
   unless the app's config chose an output itself, and then writes a flag
   file in `/tmp`;
2. runs Railpack's build command as `env NEXT_ADAPTER_PATH=<adapter> <pm>
   run build`. A step variable would reach the image's environment, where
   `next start` would look for an adapter that is not in the deployed app;
3. decides whether the app needs the standalone tree. This step runs only
   when the adapter's flag is set and a standalone server was written. It
   estimates the ext4 image the full `/app` needs the way imaged sizes an
   app layer (`rootfs.CheckCapForStaging`, amendment 1): the files'
   apparent bytes plus 10 % slack and 60 % more for the share of files under
   4 KiB, and at least 16 MiB of headroom. guest-init, which every app
   layer carries, counts as one more file of its real size (the helper
   reads its own executable). 4 MiB more covers the directories and small
   files above `/app`. The standalone tree and the build caches
   (`.next/cache` and `node_modules/.cache`, which are cache mounts and
   never deployed) are left out. The estimate is compared with the plan's
   app-layer cap, which the builder sends in the build manifest
   (`app_layer_max_mb`).
   - **Over the cap.** The step copies `.next/static` and `public` into the
     standalone tree, as Next's deployment docs do, and writes the marker
     `.next/standalone/.gregale-standalone` last.
   - **Otherwise.** The step removes `.next/standalone`, so the layer is
     the size it was before.

   The step prints its estimate and its choice to the build log. It never
   fails the build. If the copy fails, no marker is written.

The deploy inputs are unchanged. imaged ships only `/app/.next/standalone`
when three things hold: the marker is there, the start command is the plain
`next start`, and the deployment has no release command. The app then starts
with `node /app/.next/standalone/server.js`, and `HOSTNAME=0.0.0.0` is set
unless the app sets `HOSTNAME` itself. Commands other than the server, such
as `gregale run` tasks, see only the standalone tree. Only an app whose full
`/app` is over its plan's cap gets the marker, and such a deployment failed
with `app_layer_too_large` before, so nothing that worked loses its files.

An app whose full `/app` fits its plan ships it as before: the build removed
the standalone tree. A marked tree that imaged does not select stays next to
the full `/app`, which is over the cap, so the deployment fails as it did
before. This covers a release command, which may read sources, scripts or
`node_modules`.

An app that sets `output: "standalone"` itself, an older builder, the off
switch and a failed copy leave no marker, so imaged keeps the layer exactly
as it was built. A failed selection pass does too, or falls back to every
layer above the base (`next_standalone_reread`). The adapter only runs at
build time.
The standalone server ignores the `adapterPath` its embedded config
records, and verified 2026-11-24 that it serves with the file absent.

**Bun.** When any word of a shared-runtime Node app's start command runs
`bun` or `bunx` (including `NODE_ENV=production bun run start`), imaged's
`/app` filter also keeps `mise/installs/bun/**` and drops the rest of the
toolchain. Node comes from the runner base. The app starts with its own
command, with Bun's bin directory after `/app/node_modules/.bin` on PATH.

The check follows every package.json script the command reaches (flags such
as `bun run --bun start` are skipped), together with each script's pre/post
hooks. None of them may call npm, npx, yarn, pnpm, corepack or mise, which
came with mise's Node, or a script runner (`npm-run-all`, `run-p`, `run-s`,
`concurrently`) that calls npm. If one does, the layer keeps the toolchain
(`package_manager_start`).

The layer also keeps everything above the base, as before, in two more
cases:

- the artifact holds no bun executable (`bun_not_installed`);
- a start through npm, yarn or pnpm reaches a script that runs Bun
  (`bun_in_script`). Neither the package manager nor Bun is on the runner
  base, and before this ADR such an app started without Bun.

Builder nodes turn the Next.js rewrite off with `FAAS_NEXT_STANDALONE=0`.
`FAAS_SOURCE_APP_SHARED_RUNTIME=0` still turns off both changes, along
with the rest of the shared runtime.

## Consequences

- A small Next.js 16 app's layer drops from ~340 MB to ~40–45 MB on Free,
  where its full `/app` does not fit. A `create-next-app` app's full layer
  needs 545 MB, so it gets the standalone tree on Hobby too. On Pro and
  Scale it fits and ships all of `/app` as before. A small Bun app's layer drops from
  ~420 MB to ~95 MB on every plan. Both fit the Free plan with room for
  dependencies.
- A Next.js app that customizes its start (`next start -p 3000`, a custom
  server, `prestart` hooks) or pins Next 15 or older keeps its
  `node_modules` layer. `next start` reads `PORT`, so most apps can drop
  their arguments to opt in. The guide says so.
- Next.js apps whose Node version has no shared runtime (Node 22 is musl,
  others are undeclared) are unchanged; their layer still carries Node.
- Files the app reads at runtime that Next's file tracing misses are not in
  the standalone tree. This is the same contract as every standalone
  deployment. Such an app sets `outputFileTracingIncludes`, or sets its own
  `output` so the adapter leaves it alone.
- A Bun app that starts a Next.js server with `bun run start` still gets
  the standalone tree. A Bun app without it carries Bun (88 MB) plus its
  `node_modules`.

## Amendment 1 (2026-11-25): size checks found by the metal acceptance

`TestJSFrameworksFitFreeMetal` built both apps in real builder microVMs and
found three size checks that made the decision above unreachable:

- **builderd's artifact check.** builderd rejected any build artifact larger
  than the plan's app-layer cap. A shared-runtime artifact still holds the
  mise toolchain, which imaged drops, and a Next.js artifact holds the full
  `/app` beside the standalone tree. The Free Next.js build failed there at
  298 MB, before imaged chose the tree. builderd now skips that check for
  builds on a shared runner base. imaged checks the layer that ships against
  the same cap, with filesystem overhead, before anything boots. Builds on
  an app's own base keep builderd's check.
- **Filesystem overhead.** The build step first compared `du -sk` of `/app`
  with the cap. On Hobby a Next.js 16 app measured under 512 MB, so the
  build dropped the tree. imaged then sized the image of its many small
  `node_modules` files at 545 MB and refused it. The step now uses imaged's
  own estimate, as decision 3 describes.
- **guest-init.** The first estimate added a flat 24 MiB for guest-init
  after the padding. guest-init is 41.8 MB, and imaged pads it with the rest
  of the layer, so the step put a Hobby app at 506 MB where imaged needed
  545 MB. The step now counts guest-init's own size inside the estimate.
