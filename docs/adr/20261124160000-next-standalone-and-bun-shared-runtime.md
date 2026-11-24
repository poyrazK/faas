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
   unless the app's config chose an output itself;
2. runs Railpack's build command as `env NEXT_ADAPTER_PATH=<adapter> <pm>
   run build`. A step variable would reach the image's environment, where
   `next start` would look for an adapter that is not in the deployed app;
3. copies `.next/static` and `public` into the standalone tree, as Next's
   deployment docs do, then writes the marker
   `.next/standalone/.gregale-standalone`. This step changes nothing when no
   standalone server was written, and it never fails the build: a copy
   that fails leaves no marker.

The deploy inputs are unchanged. imaged decides. When the filtered `/app`
layer holds the builder's marker and the start command is the plain
`next start`, the app layer keeps only `/app/.next/standalone`. The
app then starts with `node /app/.next/standalone/server.js`, with
`HOSTNAME=0.0.0.0` unless the app sets `HOSTNAME`. A deployment with any
release command also keeps all of `/app`, because the command may read
sources, scripts or `node_modules` the standalone tree leaves out. A
standalone server without the marker keeps `node_modules`. This covers an app that sets
`output: "standalone"` itself, an older builder, and the off switch. In
those cases the static and public files were never copied. Any other case, including a failed second pass, keeps
the layer it would have had before. The adapter only runs at build time.
The standalone server ignores the `adapterPath` its embedded config
records, and verified 2026-11-24 that it serves with the file absent.

**Bun.** When a shared-runtime Node app's start command runs through `bun`,
imaged's `/app` filter also keeps `mise/installs/bun/**` and drops the rest
of the toolchain. Node comes from the runner base. The app starts with its
own command, with Bun's bin directory after `/app/node_modules/.bin` on
PATH. The start command, the script it runs and that script's pre/post
hooks must not call npm, npx, yarn, pnpm, corepack or mise, which came
with mise's Node. If one does, the layer keeps the toolchain
(`package_manager_start`). If no bun executable was in the artifact, the layer keeps everything
above the base, as before (`bun_not_installed`).

Builder nodes turn the Next.js rewrite off with `FAAS_NEXT_STANDALONE=0`.
`FAAS_SOURCE_APP_SHARED_RUNTIME=0` still turns off both changes, along
with the rest of the shared runtime.

## Consequences

- A small Next.js 16 app's layer drops from ~340 MB to ~40–45 MB and a
  small Bun app's from ~420 MB to ~95 MB, so both fit the Free plan with
  room for dependencies. Paid plans get the same smaller layers: faster
  image conversion, and smaller snapshots and replication.
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
