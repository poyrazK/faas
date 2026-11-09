# ADR-20261107193000: Function config in code

Date: 2026-11-07
Status: Accepted (owner decisions, 2026-11-07)
Amends: ADR-985 (multi-function projects), ADR-1342 (per-function settings)
Related: ADR-20261107190000 (30 s function default), the one-config-file work
(gregale.yaml canonical)

## Context

A function's handler lives in code, but everything that makes it run (its
cron, queue, bucket events, webhooks, routes, RAM, timeout, env) lives in a
`functions:` entry in `gregale.yaml`, matched to the code by name and
directory. Adding a scheduled function means editing two files and keeping
them in step; removing one leaves a stale entry behind. Vercel, Netlify and
Cloudflare let a function declare at least part of this next to its code.

## Decision

1. **A handler file may declare its config.** The CLI reads it statically
   at deploy time. It never runs customer code to find it.
   - Node (`.js`, `.mjs`): `export const config = { ... }`, or the
     typed helper `export const config = defineConfig({ ... })` from
     `@gregale/sdk-node`, which returns its argument unchanged.
   - Python (`.py`): a module-level `config = { ... }`, or the decorator
     `@function(...)` from `faas_sdk` (any `@<name>.function(...)` or
     `@function(...)` directly above `def handler`), which returns the
     handler unchanged.
2. **Literals only.** Strings, numbers, booleans, null/None, arrays/lists
   and objects/dicts. Comments and trailing commas are allowed. Anything
   else (a variable, `process.env.X`, a spread, a template with `${}`, a
   function call) is an error naming the file and line, so a config that
   would differ between deploys is never silently dropped.
3. **Same keys as `functions:`.** Keys are the `functions:` entry fields;
   camelCase is accepted and converted (`ramMb`, `requestTimeoutS`). Two
   shorthands are added:
   - `cron`: a schedule string or list of strings, becoming `kind: cron`
     triggers for the function on path `/`.
   - `queue`: a queue name or list, becoming `queue_bindings` entries named
     after the queue.
   `name`, `root` and `handler` are not allowed in code; they say where the
   code is. The config decodes through the same strict decoder and
   validation as a yaml entry, so an unknown key is an error.
4. **gregale.yaml wins.** For a function listed in `functions:`, each field
   the entry sets is kept, and the code fills only fields the entry leaves
   unset. A function whose entry has any cron trigger in `triggers:` keeps
   exactly those and ignores `cron` in code. `gregale deploy --dry-run`
   shows which file each setting came from.
5. **Discovery.** With no `functions:` key and no single-app sections in
   `gregale.yaml` (or no manifest at all), every `functions/<name>/` directory
   whose conventional handler file (`handler.js`, `handler.mjs`,
   `handler.py`) declares a config becomes a function named `<name>`.
   Directories without a config are not discovered, so existing projects
   deploy exactly as before.

## Consequences

- A new scheduled function is one file:
  `functions/digest/handler.js` with `export const config = { cron: "0 8 * * *" }`.
- Config in code is read by the CLI, so repository deploys connected
  through GitHub, which ignore `functions:` for a function app (ADR-985),
  ignore it too. Server-side reading is a follow-up.
- Go and `binary` functions keep yaml only in this slice.

## Alternatives

- **Run the module to read its config.** Exact, but it needs a matching
  local runtime and executes customer code at deploy time. Rejected.
- **Code wins over yaml.** Makes yaml useless for per-environment
  overrides. Rejected.
- **Discover every `functions/*` directory.** Would change how existing
  projects with a `functions/` folder deploy. Rejected; a config is the
  opt-in.
