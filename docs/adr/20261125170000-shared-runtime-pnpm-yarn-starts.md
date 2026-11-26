# ADR-20261125170000: pnpm and yarn starts on the shared Node runtime

Status: Accepted

Extends ADR-1500 (shared-runtime source apps) and ADR-20261124160000.

## Context

A Node app built from source on Node 24 ships only `/app` above the shared
runner base when its start needs no package manager (ADR-1500). imaged
resolves `npm start` and `npm run <script>` from `package.json` and runs the
script body with `sh`. The runner base strips npm, npx and corepack, so
nothing else can run there.

A start through pnpm or yarn was refused (`package_manager_start`). The layer
then kept every layer above the base: mise's Node, the package manager and
`node_modules`. Railpack plans `pnpm start` or `yarn start` for any app with
a `pnpm-lock.yaml` or `yarn.lock`, so these apps carried a second Node. A
small app with a few dependencies then needs Hobby.

Two more gaps showed up on the way:

- A resolved npm start script that itself calls a package manager
  (`"start": "npm run build && node server.js"`) was accepted. It exits 127
  on the runner base. Only Bun starts were checked for this.
- An exec-form entrypoint (`["npm", "start"]`) resolved only for npm.

## Decision

1. **pnpm and yarn resolve like npm.** `pnpm start`, `pnpm run <name>`,
   `yarn start`, `yarn run <name>`, and the shorthand `pnpm <name>` and
   `yarn <name>`, resolve to the script's body when `<name>` is a script in
   `package.json` and not one of the package manager's own commands
   (`install`, `exec`, `dlx`, …). The rules npm already had apply to all
   three: no `pre`/`post` scripts (npm and yarn classic run them), and no
   extra arguments (`npm_arguments`).
2. **A resolved script must not need a package manager.** If the script,
   or any script it reaches, calls npm, npx, yarn, pnpm, corepack, mise, or a
   script runner that calls npm (`npm-run-all`, `run-p`, `run-s`,
   `concurrently`), the build keeps its toolchain with the new reason
   `script_package_manager`. This applies to npm too. The check reuses the
   script walk from ADR-20261124160000; Bun keeps its own reasons.
3. **Yarn Plug'n'Play keeps the toolchain.** With PnP, Node resolves
   dependencies only through yarn's loader, which yarn adds when it runs a
   script. imaged sees `/app/.pnp.cjs` (or `.pnp.js`) while it filters the
   layer and falls back with `yarn_pnp`. The build cannot see it: the
   builder classifies from the plan and `package.json` only.
4. **Corepack's install is toolchain.** With a `packageManager` field,
   Railpack installs yarn through corepack: `/opt/corepack` (its
   `COREPACK_HOME`) and the mise Node tree `corepack enable` changed join the
   build step's deploy input. imaged drops `/opt/corepack` with the rest of
   the toolchain, and the build's plan check accepts those paths.
5. imaged also resolves an exec-form entrypoint that starts with `pnpm`,
   `yarn`, `npx` or `corepack`, as it did for `npm`.

The app runs with the runner base's Node and PATH
(`/app/node_modules/.bin` first), as npm starts already do.

## Consequences

- A pnpm or yarn (`node_modules`) app's layer drops mise's Node and the
  package manager, as an npm app's does.
- A script runs without the variables a package manager sets
  (`npm_package_*`, `npm_lifecycle_event`). npm starts already ran this way.
- An npm app whose start script calls a package manager now keeps its
  toolchain instead of exiting 127 on the runner base.
- pnpm's `node_modules` is symlinks into `node_modules/.pnpm`, which stays
  inside `/app`. A hard link out of `/app` already fails the layer filter,
  and the app keeps every layer above the base.
- `TestJSFrameworksFitFreeMetal` gains pnpm and yarn subtests on Free, and
  the journey suite deploys both fixtures.
