# `gregale dev`

`gregale dev` is a remote development loop backed by the same build,
deployment, routing, and Firecracker infrastructure as production and pull
request previews. It does not require Firecracker or KVM on the developer's
computer.

From an application directory:

```sh
gregale dev
```

Supported Node.js and Python apps automatically use their development server
with hot reload. The CLI prints the selected mode, command, and whether the
choice came from flags, the manifest, detection, or server eligibility. Use
`gregale dev --watch=false` for a production-shaped developer build.

The normal `dev` entry point runs the local source checks used by `dev setup`
before provisioning. Blocking source errors include a `doctor --path` command;
warnings remain visible. Source and app defaults come from the selected source
directory, including with `--path`. The app default supplies the developer
session name; the session still uses an isolated developer app and resources.
On exit, the CLI explains the lease and prints exact `dev info` and `dev --stop`
commands retaining the source, session name, and connection profile.

## Save setup for the next session or a teammate

Preview the portable choices, then save them:

```sh
gregale dev setup --env-file .env.dev --postgres --ttl 72h --save --dry-run
gregale dev setup --env-file .env.dev --postgres --ttl 72h --save
gregale dev
```

`--save` records the effective lease, database and bucket choices, and relative
env-file and service-override-file references in `gregale.yaml` (or the existing
`gregale.yml`). It preserves unrelated manifest settings, comments, and file
permissions. It never exports file contents or credentials. Referenced files must
be inside the source directory; each developer supplies their own values. Session
names remain local choices. Existing hosting, seed, debug, and watch configuration
continues to work.

Saving requires valid local source checks but no login or network access.
`--dry-run` requires `--save` and writes nothing. Add `--start` to save and then
start the developer loop after authentication; it cannot be combined with
`--dry-run`. JSON output adds a `saved_config` object describing the selected
values and destination. TOML manifests require an explicit migration before
using this YAML writer.

## Run HTTP apps on this machine

From an existing Express, FastAPI, or Go HTTP project:

```sh
gregale dev --local
gregale dev --local --offline --postgres --bucket uploads
```

The CLI detects the development command, falling back to the ordinary start
command. It runs the app behind `http://localhost:8787`, passes its listening
port as `PORT` and loopback address as `HOST`, and restarts the process group
after source edits settle. The public local URL stays the same across restarts.
Detected FastAPI, Flask, and Django commands receive explicit host/port options.
Project tools in `node_modules/.bin`, `.venv/bin`, and `venv/bin` are on PATH;
install your application's dependencies before starting it.

Override detection for a custom command (which must listen on `PORT`):

```sh
gregale dev --local --watch-command 'node server.js'
gregale dev --local --watch-command 'go run ./cmd/api' --port 9000
```

The command precedence is `--watch-command`, `dev.watch.command` in
`gregale.yaml`, the detected framework development command, then the inferred
start command. `--no-reload` disables CLI restarts; an explicitly selected
command can still run its own watcher. App logs stream to the terminal. A crash
keeps the proxy available with an unavailable response; fix the source and save
to restart. Ctrl-C stops the app and its child processes before closing borrowed
resources. Managed HTTP app processes currently require Linux or macOS.

Apps use the same remote or offline Postgres/bucket setup described below.
`--env-file` supplies local configuration; nothing is uploaded and remote secret
values are not exported. Runtime host/port settings are reserved for the local
proxy. Local runs do not reproduce microVM isolation or snapshot behavior.
Function projects retain their existing runner and local trigger behavior.

Local apps and functions use `dev.env_file` from `gregale.yaml` unless
`--env-file` is supplied. Manifest paths are relative to the source directory;
flag paths are relative to the directory where you invoke the CLI.
`--env-file=` disables the manifest default. The selected path stays fixed
for the run; restart the CLI to select a different file.

Saving that file restarts the app or function workers after edits settle,
including when the file is ignored or outside the source directory. Each
restart rebuilds the environment from the original shell and resource bindings,
then the file, then each function's declared `env:`. Removing a key restores
the underlying setting; an empty or comment-only file clears all file
overrides. Invalid, missing, or symlinked files leave the current processes
running and report an error without echoing values. Fix the file and save to
retry. `--no-reload` disables these CLI restarts too.

## Run functions on this machine

`gregale dev --local` runs the project's Node, Python and Go functions on
your computer, with the same adapters and runner code a deployed function
uses, and reloads functions after source edits settle. Nothing is
uploaded and no login is needed:

```sh
gregale dev --local
```

```
Serving 3 function(s) on this machine (ctrl-c to stop):
  web       http://web.localhost:8787     node   functions/web
  users     http://users.localhost:8787   python functions/users
  digest    http://digest.localhost:8787  node   functions/digest  cron 0 8 * * *
  http://localhost:8787 routes like the entry address (entry: web)
```

- Each function has its own address, `http://<name>.localhost:8787`, as it
  has its own host on Gregale. With an `entry: true` function,
  `http://localhost:8787` routes by the functions' `routes:`, and a project
  with one function is also served there. `--port` picks another port.
- Requests carry the platform's `x-faas-invocation-id` and
  `x-faas-deadline-ms` headers, and client-sent `x-faas-*` headers are
  dropped, as on Gregale.
- If this directory has a developer environment (`gregale dev` created
  one), local functions use its resources:
  - **Postgres**: the binding's variable (usually `DATABASE_URL`) points at a
    local tunnel to the session's database. The CLI makes the TLS connection
    to the database itself and verifies it as the binding asks, so every
    driver works with the plain local URL. The tunnel starts a task VM, so it
    needs app tasks on your plan.
  - **Buckets**: each bucket's `GREGALE_S3_*` settings use a credential made
    for this local session and revoked when you stop.
  - **Secrets** never leave Gregale. The CLI lists the session's secret
    names that nothing local sets; put their values in an `--env-file`.

  `--no-remote` skips all of this. Without a login or a developer
  environment, functions run with local settings only.
- `--offline` runs the resources on your machine in Docker instead, with
  no Gregale account involved:

  ```sh
  gregale dev --local --offline --postgres --bucket uploads
  ```

  `--postgres` starts Postgres 16 and sets `DATABASE_URL`; each `--bucket`
  creates a bucket in a local S3 gateway (Versity, `versity/versitygw`) and
  sets its `GREGALE_S3_<NAME>_*` settings. `dev.postgres` and
  `dev.buckets` in `gregale.yaml` are read too. Data stays in Docker
  volumes for this project between runs; `--reset` wipes them. The
  containers listen on loopback only and stop when you stop.
- Settings layer in this order, later winning: your shell, the session's
  resources, `--env-file`, then each function's `env:` in `gregale.yaml`.
- Run a cron or an async call as Gregale would, with
  `x-faas-invocation-source` set:

  ```sh
  gregale dev trigger --local cron digest
  gregale dev trigger --local invoke users /users/7 --data '{"id":7}'
  ```

- `node`, `python3` and `go` must be installed for their functions. Go
  functions are rebuilt on save.

A local run has no memory cap, no pause between requests and no snapshot.
Use `gregale dev` without `--local` to check those on real infrastructure.

## The development loop

The CLI detects the application shape, uploads the current working tree
(including uncommitted changes), waits for the real build, prints a stable URL,
and watches deployable files for the next change. Watching continues while a
build is running. If another settled edit arrives, Gregale cancels the obsolete
deployment and builds the newest source instead of letting old saves queue up.

The first sync uploads a complete source snapshot. Later edits transfer only
new, changed, and deleted archive entries when that is smaller than the full
snapshot. The server reconstructs and validates the complete source before
building, so incremental transfer does not create a second build path. Its
source cache is disposable: after a restart, eviction, or cross-host request,
the CLI automatically resends the complete snapshot. A new `gregale dev`
invocation also starts safely with a complete sync.

Railpack and Dockerfile developer builds keep their Firecracker VM isolation
but reuse a tenant- and workspace-scoped BuildKit dependency cache between
syncs. When the runtime, Dockerfile, and lockfiles are unchanged, matching
layers are restored; a dependency, build-input, or runtime change is validated
by BuildKit and rebuilds only the invalidated layers. A Railpack build whose
plan and dependency inputs are unchanged keeps the cache instead of saving it
again. Cache import, export, and validation failures fall back to a cold build. The CLI prints whether the
dependency cache was restored and the total time until the new version is
live.

```sh
gregale dev --once             # sync once, do not watch
gregale dev --path apps/api    # select one workspace application
gregale dev --go-command worker # build cmd/worker of the Go module at --path
gregale dev --all              # run every deployable workspace application
gregale dev --name payments    # choose the stable project identity
gregale dev --stop             # tear down the project's environment
gregale dev status             # show developer-environment quota usage
gregale dev history            # inspect recent edit-to-live timings and SLO guidance
gregale dev setup              # preflight a project and print the exact next command
gregale dev setup --start      # preflight, provision, and start the developer loop
gregale dev history --limit 50 # show a larger bounded history
gregale dev info               # show the URL, app slug, lease, and database without renewing
gregale dev trigger invoke --path /orders  # call the developer app through the invoke API
gregale dev trigger cron /jobs/nightly     # fire a cron route declared in gregale.yaml
gregale dev trigger delayed-task --delay 5m --path /reminders # schedule a delayed task
gregale dev --no-logs          # keep the watcher quiet for scripts
gregale dev --open             # open the verified dev URL after the first live sync
gregale dev --env-file .env.dev # opt in to syncing local config as secrets
gregale dev --service-override-file .env.services.local # opt in to service URL overrides
gregale dev --postgres         # provision an isolated database and inject DATABASE_URL
gregale dev --postgres --postgres-region eu-central-1 # choose database placement
gregale dev --postgres --postgres-seed "npm run seed" # seed a new database once
gregale dev --postgres --postgres-seed "npm run seed" --reseed # run the seed again
gregale dev --once --json      # emit mode and edit-to-live NDJSON records
gregale dev --ttl 72h          # keep the environment 72h after the latest sync
```

For a repeatable team setup, put non-secret developer defaults in the
project's `gregale.yaml`:

```yaml
dev:
  env_file: .env.dev
  service_override_file: .env.services.local
  postgres: true
  postgres_region: eu-central-1
  ttl: 72h
  postgres_seed: npm run seed
```

These paths are relative to the selected source root. The files must still be
created locally, are validated before any remote mutation, and remain excluded
from the source archive. Explicit CLI flags take precedence over the manifest;
`gregale dev setup` shows the resulting effective command without starting or
mutating the environment.

The dashboard at `/dashboard/developers` lists the active environments for
the signed-in account. It shows each stable URL, runtime, current instance
state, latest sync result, useful links for logs/request analytics/config, and
the lease expiry. The dashboard uses the same account-scoped developer
environment list as the CLI, and developer environments have their own quota.
For each environment it also shows recent sync count, SLO compliance, p50/p95
edit-to-live timing, and a phase-level hint when the latest sync regresses.

`gregale dev history` provides the same bounded view in the terminal. Use
`--path` or `--name` to select a workspace and `--json` for the summary and
receipt list as one stable object. History is keyed by deployment ID, so a
retry cannot double-count a sync.

`gregale dev info` shows the selected environment's stable URL, the backing
`dev-*` app slug that every other `gregale` command accepts, the lease expiry,
and the safe managed PostgreSQL state. It reads
`GET /v1/dev/sessions/{project}` and, unlike starting `gregale dev`, never
renews the lease or provisions anything. `--json` emits the session object.

`gregale dev trigger` exercises request-driven and async paths against the
developer app without copying its slug. Put `--path` and `--name` (source
selection) before the verb; everything after the verb belongs to the
delegated command, so `invoke --path` still means the URL path:

- `gregale dev trigger invoke [invoke flags]` runs `gregale invoke` against
  the developer app, including `--async`.
- `gregale dev trigger cron [ROUTE]` fires a cron trigger declared for this
  app in `gregale.yaml`. Manifest crons are keyed to the production app slug,
  so they never run on a schedule in a developer environment. The trigger
  sends the request a scheduled cron would send: a body-less `POST` to the
  route, synchronously through the invoke API. `ROUTE` is optional when the
  app declares exactly one cron route.
- `gregale dev trigger delayed-task [delayed-task add flags]` runs
  `gregale delayed-task add` against the developer app.

A stray app slug or `--app` is rejected rather than silently retargeting
another app. If no environment exists for the source directory (or its lease
expired), the commands say so and point to `gregale dev`.

`gregale dev setup` is the first-run preflight. It is local and read-only: it
uses the same source-shape detector as `gregale dev`, validates
`gregale.yaml`, checks an explicitly selected `--env-file` without printing
values, and reports local source errors before creating a remote environment.
It also reports whether this machine is logged in and prints the exact
copy-paste command for the next step. `--start` hands the validated plan to
`gregale dev`, which provisions the stable URL, waits for the first sync to
become live, and watches for changes unless `--once` is supplied.

Setup checks required runtime keys against the selected env and service-override
files and planned database/bucket bindings. An unselected `.env` or a local
declaration does not prove that a remote app will receive its values. Optional
references and platform-provided settings retain their existing exemptions.

The receipt's `configuration` object lists `required`, `provided`, `planned`,
`empty`, `missing`, `unverified`, and `existing_values_unverified` names.
Setup stays offline: unresolved keys are `unverified`, and `ready` is false.
`--start` can still hand that plan to `dev` for verification. Known empty
required values block the handoff until fixed.

Before each source upload, `dev` checks unresolved names in the isolated
developer app's default scope. Missing keys or failed metadata reads block
upload/build; a linked production app cannot satisfy the check. Existing names
do not verify their values, and planned bindings do not prove delivery or
resource readiness. The selected-file values checked are the same snapshot
used for that sync. Only key names appear in reports.

With `--json`, each attempt emits a `developer_configuration` NDJSON record
before config/source sync. Setup includes the same fields under
`configuration`; human output shows the names grouped by their source.

`--open` launches the stable developer URL in the default browser after the
first successful sync. It opens at most once per session, including with
`--once`; a browser-launch failure is non-fatal and leaves a copyable URL in
the terminal. JSON output never launches a browser.

`--env-file` is explicit and additive/update-only. Each non-empty, non-comment
`KEY=VALUE` entry is written to the developer app's sealed default secret
scope; omitted keys are left untouched. The CLI compares and reports key names
only, never prints values, and refreshes the secrets before the first deploy.
Changes to the file trigger the same debounced redeploy as source edits. The
file itself is excluded from the source archive, including when it lives inside
the watched directory. Use `gregale env unset --app <slug> KEY` when a key
must be removed intentionally.

`--service-override-file` is a narrower companion for local service wiring. It
accepts only absolute connection URLs for `DATABASE_URL`, `REDIS_URL`,
`MONGO_URL`/`MONGODB_URL`, `RABBITMQ_URL`, and `NATS_URL`, with the matching
connection scheme. The file is validated before the first remote mutation,
excluded from the source archive, and synced only to the stable developer app's
sealed default secret scope. It never changes production bindings or deletes
omitted keys. Because `gregale dev` runs remotely, loopback hosts such as
`localhost` and `127.0.0.1` are rejected; use a reachable development service,
a tunnel, or `--postgres` instead. `gregale dev setup` performs the same
validation and includes the option in its copy-paste start command.

`--postgres` provisions one development-class, scale-to-zero PostgreSQL
database for this local workspace and binds its sealed credential to the
developer app as `DATABASE_URL`. The database is reused on later starts, and
the binding is refreshed asynchronously if the provider is still provisioning.
The safe database and binding states appear in human output and `--json`
receipts; credentials and connection URLs never do. `--stop` and the developer
lease clean up the binding and database together.

`--postgres-seed` (or `dev.postgres_seed`) loads development data into that
database. The CLI never receives database credentials, so the command runs
inside the developer app instead, as a one-off
app task against the live
deployment with the app's secrets and `DATABASE_URL` binding. After the first
live sync, the CLI waits for the binding to become ready, runs the command
through the app shell, and prints its bounded output prefixed with `seed |`.

The seed runs once per provisioned database. Completion is recorded in the
Gregale config directory, keyed by the database ID, so a database recreated
after `--stop` or lease expiry is seeded again while later `gregale dev` runs
against the same database skip it. `--reseed` runs it again unconditionally.
The marker is local to this machine, so write seeds that are safe to repeat.

A failed seed does not fail the live sync: the diagnostic
(`developer_seed_failed`, phase `seed`) explains what happened, and the seed is
retried after the next successful sync. With `--once`, a failed seed makes the
command exit non-zero. With `--json`, the result is emitted as a
`{"event":"developer_seed",...}` object containing the database, task ID,
status and exit code — never the command output or credentials.

Watch mode attaches one app-level runtime log stream after the first live sync.
It follows the stable developer URL across later redeploys, prefixes lines with
their stream (`runtime stdout` or `runtime stderr`), and reconnects after a
transient API or scheduler interruption. `--once` remains finite and does not
attach the stream.

Each sync also ends with a compact phase summary, for example
`sync=1.2s · cache=0.3s · build=2.4s · boot=1.1s · ready=0.4s · route=0ms · edit-to-live=5.4s · slo<=15s (met)`.
The source transfer is measured by the CLI; the cache, build, boot, and
readiness values come from the deployment stage timings. Build output is
prefixed with `build |` so it stays distinguishable from the app-level
`runtime |` stream in the same terminal.

With `--json`, the confirmed session first emits a `developer_mode` NDJSON
event with `mode` (`watch`, `build`, or `function`), `source`, and the selected
development `command` when applicable. A server fallback includes `reason` and
`detail`. This decision is local CLI output and is not stored in sync-history
telemetry. `dev setup --json` includes the planned choice under `development`;
RAM and access requirements are checked when the session starts.

Each successful sync then emits one `developer_sync` receipt. It
contains the deployment id, total edit-to-live milliseconds, the 15-second
cached-edit target, a `within_slo` boolean, and the phase timings. Receipts are
NDJSON so a long-running watcher can be consumed incrementally; no source,
secret, or runtime-log content is included.

Receipts also carry a `dev_patch` object. It reports whether the edit could
be applied to the running environment as a live source patch instead of a
rebuild, comparing the
newest source with the source of the build that is live. `eligible` is true
when that build copied its source into the image unchanged (for example a Node
app without a `build` script, or Python with `requirements.txt`) and the
changes touch no build input such as `package.json`, a lockfile,
`requirements.txt`, `railpack.json`, or `gregale.yaml`. Otherwise `reason`
says why, for example `build_command`, `rebuild_input_changed`,
`no_base_manifest` (the live build predates this feature), or
`no_live_build`. `changed_paths` and `patch_bytes` describe the changes.

Applying eligible patches to the running environment is not enabled for every
account yet. Until it is, `dev_patch` is a
measurement only and the normal developer build always runs; when enabled, the
build still runs and replaces the patched environment once it is live.

When a sync publishes a live patch, `dev_patch.generation` is set and the
watcher follows its delivery while the build continues. As soon as the
running environment applies it, the terminal prints
`Live patch applied in 0.8s; the app is restarting with your edit while the full build continues.`
The phase summary then includes `patch=0.8s` (edit-to-patch, measured on your
machine), and `gregale dev history` shows `(live patch 0.8s)` beside the sync.
A patch that fails to apply is reported with its reason, and the build still
delivers the edit. With `--json`, the watcher emits a
`{"event":"developer_patch",...}` NDJSON record with the generation, `state`
(`applied` or `failed`), `edit_to_patch_ms`, and `apply_ms`; the
`developer_sync` receipt includes the same `patch` phase.

When a sync cannot be live-patched, the terminal says why and what would make
the app patchable, for example
`Full rebuild instead of a live patch: your app has a build step, so edits go through the build. Run gregale dev --watch to hot-reload it with its development server (npm run dev) instead ...`
or that a dependency or lockfile changed. A reason is printed when it first
appears and again only after a sync with a different outcome, so an app with a
build step hears it once per session. Expected cases (the first sync, a running
version from before live patches) stay quiet. With `--json`, the reason is the
`dev_patch.reason` field of the `developer_sync` receipt.

Failed syncs include a developer diagnostic in the same terminal. Deployment
stage failures reuse the platform error code and explain the failing phase,
the next action, and the deployment log command. The runtime stream also
recognizes high-confidence startup failures such as missing modules, bind
errors, upstream connection refusals, panics, and runtime OOMs; each code is
reported once per watch session, including after a log-stream reconnect.
Connection refusals to localhost are reported as dependency failures; normal
local development-server URLs do not imply a bind failure.

With `--json`, each diagnostic is one NDJSON line with
`"event":"developer_diagnostic"`, the developer `app`, and `source_dir`.
Runtime diagnostics remain active without emitting raw runtime log lines;
`--no-logs` disables the runtime follower, and `--once` does not start it.
Recovery log commands explicitly target the developer app and include the
failed deployment ID when known. Runtime messages without a deployment ID
select that developer app's runtime logs. In `--all`, records also carry
`dev_project` and `dev_path` so editor integrations can route the guidance.

The URL is stable for an account, local developer installation, and source
directory. Teammates and separate clones or worktrees therefore get independent
environments, while repeated runs from the same source directory resume the
same one. The non-secret local identity lives in the Gregale config directory;
`FAAS_DEVELOPER_ID` can override it with 32 lowercase hexadecimal characters
for reproducible automation.

Each sync renews the environment's lease; the existing preview janitor tears
down an expired environment. The lease is 24 hours unless `--ttl` (or `dev.ttl`
in `gregale.yaml`) chooses another Go duration such as `8h` or `72h`. A lease
must be at least one hour and at most the plan's developer lease maximum (see
[Plans](plans.md#developer-environments)); a longer request fails with
`plan_limit_developer_lease` before the environment is created or refreshed.
The CLI validates the value locally and prints the effective lease when the
environment starts; `gregale dev setup` includes it in the start command.
Because every sync renews the lease with the requested value, the most recent
start wins: running without `--ttl` returns the environment to 24 hours.
Stopping the watcher with Ctrl-C leaves the environment available—use `--stop`
from the same source directory when it should be removed immediately.

## Debug the remote environment

`gregale dev --debug` (or `debug: true` in the `dev:` block of
`gregale.yaml`) starts the Node.js inspector inside the developer environment
and exposes it on your machine:

```sh
gregale dev --debug                    # inspector on 127.0.0.1:9229
gregale dev --debug --debug-port 9339  # pick another local port
```

After the first live sync the CLI prints
`Debugger listening on 127.0.0.1:9229`. Attach any Node.js debugger to that
address, for example a VS Code configuration:

```json
{
  "type": "node",
  "request": "attach",
  "name": "Attach to gregale dev",
  "address": "127.0.0.1",
  "port": 9229,
  "remoteRoot": "/app",
  "localRoot": "${workspaceFolder}",
  "restart": true
}
```

or open `chrome://inspect` and add `127.0.0.1:9229`. Each debugger connection
is its own tunnel through the API (`GET /v1/apps/{slug}/debug`, a WebSocket
served by the gateway), authenticated with your CLI credentials; the
inspector port is never published on the app's URL. Attaches and detaches are
recorded in the audit log (`dev.debugger.attached` / `dev.debugger.detached`).
Only `gregale dev` environments accept a debugger: the platform marks them
with `FAAS_DEVELOPER_ENVIRONMENT=1`, and the inspector switch is ignored on any
other app.

Limits: up to four connections per environment, counted per gateway (so a few
more can be admitted in total); a connection closes after 30 minutes without traffic or
after 256 MB in either direction. Reattach to continue. The CLI caches the
`/json` discovery requests that debuggers poll for two seconds, so they do not
open a tunnel each.

While a debugger is attached the environment does not park, and a process
paused at a breakpoint is not restarted for failing its liveness probe. That
exemption holds only while a debugger connection is open: with `--debug` on
and nothing attached, a hung process is restarted as usual. A request held at
a breakpoint is still subject to the edge request deadline, so resume within
it. A live patch restarts the process and drops the debugger; with
`"restart": true` VS Code reconnects on its own. Running `gregale dev` without
`--debug` turns the inspector off again.

The inspector opens in the app's own Node.js process, not in a package-manager
or watcher wrapper (`npm`, `yarn`, `pnpm`, `nodemon`, `tsx`, `next`,
`node --watch`, …), so an `npm start` script still debugs the server it
starts. Functions run on a single worker while a debugger is attached, and a
worker held at a breakpoint is not replaced when its request times out.

Only Node.js workloads are supported so far: Bun and Deno projects are
refused. `--debug` cannot be combined with `--once` or `--stop`; a
`debug: true` in `gregale.yaml` is skipped (with a note) for `--once` and for
non-Node workloads. With `--all`, set `debug: true` and a distinct
`debug_port` in each app's `dev:` block:

```yaml
dev:
  debug: true
  debug_port: 9230
```

## Watch mode for apps with a build step

Live patches (above) only apply to apps that run their source files directly.
For an app with a build step, such as TypeScript, Next.js, Vite or a uv
Python project, every save otherwise waits for a full build.
Plain `gregale dev` automatically runs a detected Node.js or Python development
server when the source and environment support it. Source edits reach that
server through live patches while the background developer build converges:

```bash
gregale dev                                      # automatically picks the fast path
gregale dev --watch=false                        # use a production-shaped build
gregale dev --watch                              # require watch mode; report unsupported environments
gregale dev --watch-command "next dev"           # or an explicit command
gregale dev --all                                # each app chooses its own mode
gregale dev --all --watch=false                  # disable watch mode for every app
```

or in `gregale.yaml`:

```yaml
dev:
  watch: true          # or: watch: { command: "next dev" }
```

Set `dev.watch: false` to disable automatic selection for the project. Explicit
CLI flags override the manifest, and the manifest overrides detection. An
explicit `--watch-command` enables watch mode; it cannot be combined with
`--watch=false`. Settings are resolved when starting the loop; restart it to
change the selected mode.

Detection picks the following commands:

| Your app | Command |
| --- | --- |
| a `dev` script in `package.json` | `npm run dev`, or `pnpm` / `yarn` / `bun run dev` when the `packageManager` field or lockfile says so |
| no `dev` script: Nuxt / Astro / SvelteKit / React Router / Remix with Vite / Next.js / Vite | `nuxt dev` / `astro dev` / `vite dev` / `react-router dev` / `remix vite:dev` / `next dev` / `vite` |
| Django (`manage.py`) | `python manage.py runserver` |
| FastAPI | `uvicorn <module>:<app> --reload`, for the module that creates `FastAPI()` |
| Flask | `flask --app <module>:<app> run --debug`, for the module that creates `Flask()` |

The development server can listen wherever it likes: on `localhost` only,
or on its own port (Vite's 5173, Astro's 4321). In a watch-mode environment
Gregale forwards the app's port to the port the server actually opened, so no
`--host` or `--port` flags are needed. With `gregale dev --all`, each child reads
its own manifest and detects its own command. Functions keep their runner;
Go apps, Dockerfile builds, and apps without a detected development command
keep production-shaped builds. A parent's explicit `--watch=false` is passed
to every child and overrides per-app `dev.watch` settings.

The developer environment's hostname is available to the development server
as `GREGALE_DEV_HOST`. Vite-based servers accept it automatically (Gregale
sets Vite's `__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS`). Two frameworks need it
in their own configuration:

- **Next.js** serves hot reload only to origins listed in
  `allowedDevOrigins`, which it reads from `next.config` alone. In a terminal,
  `gregale dev` offers to add it for watch-mode sessions: it creates `next.config.mjs`, or
  edits a config that exports a plain object, and otherwise prints the line
  to add:

  ```js
  allowedDevOrigins: process.env.GREGALE_DEV_HOST ? [process.env.GREGALE_DEV_HOST] : [],
  ```

  The value is empty outside the developer environment.
- **Django** answers 400 to hosts outside `ALLOWED_HOSTS`; add
  `os.environ.get("GREGALE_DEV_HOST", "")` to it. The CLI reminds you when no
  Python file mentions `GREGALE_DEV_HOST`.

The developer build then installs your dependencies as usual, skips the
production build, ships your source unchanged and starts the development
command (Node.js apps with `NODE_ENV=development`; Python apps with `src/` on
`PYTHONPATH`, because the project is not installed into the virtual
environment). Source edits reach the running server as live patches without
restarting it, and the server's own watcher recompiles and reloads them.
Changing `package.json`, `pyproject.toml`, a lockfile, `railpack.json` or
`gregale.yaml` still triggers a full developer build, which reinstalls
dependencies; a build that changes none of them keeps the dependency cache
instead of saving it again.

Watch mode needs:

- a Node.js or Python app built without a Dockerfile (a Dockerfile owner can
  write a development stage instead);
- a command: a `dev` script, a framework from the table above, or
  `--watch-command`;
- at least 512 MB of RAM for the developer environment;
- authentication on the developer environment, because development servers
  serve source maps and error overlays.

For an automatic choice, a `dev_watch_unsupported` rejection selects the existing
production-shaped build and prints the server's explanation. It does not
increase RAM or change authentication. Other failures, including transport and
5xx errors, do not trigger another submission. If an older server accepts the
request but reports watch mode off, the CLI uses that reported build mode.
Explicit flags or manifest watch settings surface unsupported or unconfirmed
watch settings as errors instead of silently changing the requested mode.

A watch-mode
environment runs your development server, not your production build, so check
production behaviour with `gregale deploy` or a pull-request preview. Run
`gregale dev --watch=false` to go back to production-shaped developer
builds; the change applies from the next build.

## Run every app in a workspace

`gregale dev --all` starts one developer loop per deployable workspace,
convention, or Compose member below `--path` (default: the current
directory), using the same detection as `gregale start` and `gregale scan`.
A Compose service counts when it builds from a source directory (`build:`);
image-only services such as `postgres` or `redis` are not started. In a Go
module with several `cmd/<name>` commands and none at its root, each command
is its own app, built from the module root with `--go-command <name>`. Each app gets its own
developer environment, stable URL, and lease, named after its workload
(`apps/api` becomes `api`). When the root has deployable members it is treated
as the workspace container and not started; a root with no members is the
single app.

```sh
gregale dev --all              # watch every app
gregale dev --all --once       # sync every app once
gregale dev --all --stop       # tear down every app's environment
gregale dev --all --once --json
```

Each app runs as its own `gregale dev --path DIR --name PROJECT` loop. Its
output is prefixed with the project name (`[api] build | …`), one app's failed
build or crash never stops the others, and Ctrl-C stops every loop. With
`--json`, every receipt and diagnostic is emitted as one NDJSON line carrying
`dev_project` and `dev_path`. The command exits 0 only when every loop exits 0.

`--once`, `--stop`, `--no-logs`, `--open`, `--postgres`, `--postgres-region`,
and `--ttl` apply to every app. `--name`, `--env-file`,
`--service-override-file`, `--postgres-seed`, and `--reseed` are rejected with
`--all`; put per-app values in each app's own `gregale.yaml` `dev:` block, which
its loop reads from its source root. Before creating anything, the CLI checks the account's developer
environment budget: an app whose environment already exists reuses its slot,
and the command fails with the shortfall when the new environments would not
fit.

Developer environments have a separate per-plan quota from production apps and
pull-request previews. They are still backed by the same preview lifecycle and
lease; `gregale dev status` reports the account-wide budget so a local
workspace cannot unexpectedly block a deploy or PR preview.

### Call the other apps

The apps started by one `gregale dev --all` call each other by name, the same
way deployed project services do:

```sh
curl http://api.internal:10081/health      # the api app's developer environment
curl http://api.svc.gregale:10081/health    # the same
```

Each app is called by its service name, as in production: the Compose service
or workspace member name, such as `ui` or `api`. A name that is not a DNS name
(`Billing_API`) cannot be called in production either; in the group the app is
called by its developer project name (`billing-api`), and the CLI says so when
it starts. A call by name reaches only the apps of the same `gregale dev --all`
and never a deployed app: a name that is not running in the group fails
instead of reaching production. Call a deployed service through its public URL.

An app that declares `depends_on` services running in the group gets the same
`GREGALE_SERVICE_<NAME>_URL`, `GREGALE_SERVICE_<NAME>_HTTPS_URL` and
`GREGALE_SERVICE_<NAME>_HOST` variables a deployed project service gets, set
as developer config on its environment. A dependency outside the group, such
as a database image, gets none; use `--postgres` for a database.

The group's environments belong to the `--all` root: running
`gregale dev --path apps/api` on its own starts a separate environment outside
the group, whose `<service>.svc.gregale` calls reach the account's deployed
app of that name (subject to its preview service-call policy). Environments
left by a `dev --all` from an older CLI are not reused and expire with their
lease.
