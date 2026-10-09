# `gregale dev`

`gregale dev` is a remote development loop backed by the same build,
deployment, routing, and Firecracker infrastructure as production and pull
request previews. It does not require Firecracker or KVM on the developer's
computer.

From an application directory:

```sh
gregale dev
```

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
by BuildKit and rebuilds only the invalidated layers. Cache import, export, and
validation failures fall back to a cold build. The CLI prints whether the
dependency cache was restored and the total time until the new version is
live.

```sh
gregale dev --once             # sync once, do not watch
gregale dev --path apps/api    # select one workspace application
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
gregale dev --once --json      # emit one machine-readable edit-to-live receipt
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
the watched directory. Use `gregale secrets unset --app <slug> KEY` when a key
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
[app task](adr/230-deployment-attached-app-tasks.md) against the live
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

With `--json`, each successful sync emits one `developer_sync` receipt. It
contains the deployment id, total edit-to-live milliseconds, the 15-second
cached-edit target, a `within_slo` boolean, and the phase timings. Receipts are
NDJSON so a long-running watcher can be consumed incrementally; no source,
secret, or runtime-log content is included.

Receipts also carry a `dev_patch` object. It reports whether the edit could
be applied to the running environment as a live source patch instead of a
rebuild ([ADR-740](adr/740-developer-live-source-patch.md)), comparing the
newest source with the source of the build that is live. `eligible` is true
when that build copied its source into the image unchanged (for example a Node
app without a `build` script, or Python with `requirements.txt`) and the
changes touch no build input such as `package.json`, a lockfile,
`requirements.txt`, `railpack.json`, or `gregale.yaml`. Otherwise `reason`
says why, for example `build_command`, `rebuild_input_changed`,
`no_base_manifest` (the live build predates this feature), or
`no_live_build`. `changed_paths` and `patch_bytes` describe the changes.

Applying eligible patches to the running environment is operator-gated while
it completes native acceptance. Until it is enabled, `dev_patch` is a
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

Failed syncs include a developer diagnostic in the same terminal. Deployment
stage failures reuse the platform error code and explain the failing phase,
the next action, and the deployment log command. The runtime stream also
recognizes high-confidence startup failures such as missing modules, bind
errors, upstream connection refusals, panics, and runtime OOMs; each code is
reported once per watch session. With `--json`, diagnostics are emitted as
`{"event":"developer_diagnostic",...}` objects so editor integrations can
surface the same guidance without parsing terminal prose.

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
and exposes it on your machine
([ADR-741](adr/741-developer-debugger-attach.md)):

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
is its own tunnel through the API, authenticated with your CLI credentials;
the inspector port is never published on the app's URL. Only `gregale dev`
environments accept a debugger, and up to four connections per environment.

While a debugger is attached the environment does not park, and a process
paused at a breakpoint is not restarted for failing its liveness probe. A
request held at a breakpoint is still subject to the edge request deadline,
so resume within it. A live patch restarts the process and drops the
debugger; with `"restart": true` VS Code reconnects on its own. Running
`gregale dev` without `--debug` turns the inspector off again. Only Node.js
workloads are supported so far; `--debug` cannot be combined with `--once`,
`--stop`, or `--all`.

## Run every app in a workspace

`gregale dev --all` starts one developer loop per deployable workspace or
convention member below `--path` (default: the current directory), using the
same detection as `gregale start` and `gregale scan`. Each app gets its own
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

Developer environments are not wired to each other. A call to
`<service>.svc.gregale` from a developer environment targets the account's
deployed app of that name (subject to its preview service-call policy), not the
sibling developer environment, because developer sessions are not part of a
project preview scope. Point an app at a
sibling with that sibling's printed developer URL through your app's own
configuration, or use [Dev Bridge](dev-bridge.md) against a named development
environment.

Developer environments have a separate per-plan quota from production apps and
pull-request previews. They are still backed by the same preview lifecycle and
lease; `gregale dev status` reports the account-wide budget so a local
workspace cannot unexpectedly block a deploy or PR preview.
