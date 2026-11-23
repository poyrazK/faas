# Fix a failed deploy

A failed deploy never replaces what is serving: the previous release keeps
answering traffic until a new one passes its checks. This page takes you from
the failure to the fix, starting with the failures people hit most.

## Find out why it failed

`gregale deploy` prints the failure's code, what went wrong and the next step
when it stops. To read it again later:

```bash
gregale inspect my-api --errors        # the latest failed deployment's explanation
gregale release list my-api --limit 5  # recent deployments and their status
gregale release logs v42 --app my-api  # one deployment's full build and deploy log
gregale logs my-api --since 15m --level error   # the app's own output
```

The code, such as `app_startup_crash`, is the quickest way into the sections
below. Every code is also listed in the [error code reference](error-codes.md).

Before you deploy again, check the source locally:

```bash
gregale doctor           # flags loopback binds, a `/` health path and other known traps
gregale deploy --dry-run # shows the detected framework, start command and port
```

## The app exited during startup

**Code:** `app_startup_crash`

The process stopped before it became ready. The error's detail carries the
last lines the app printed; `gregale release logs` has all of them. The usual
causes:

- **A missing environment variable or secret.** The app read a variable that
  is not set. Add it with `gregale env set --app my-api NAME=value`, or for a
  credential see [Delivered secrets](secrets.md), then deploy again.
- **An import or syntax error, or a missing entry file.** Run the start
  command locally from a clean checkout. `gregale deploy --dry-run` shows the
  command Gregale will run.
- **The process exits when it is done.** A web app must keep running. A start
  command that runs a script and exits with code 0 still fails, because
  nothing is left to serve. For run-to-completion work, use a
  [job](jobs.md) or a [cron](crons.md) instead.

## The app never became ready

**Codes:** `app_startup_timeout`, `app_not_listening`, `app_loopback_bound`

Gregale waits for the app to accept connections on its port, or to answer its
health path with a 2xx, within the startup deadline (30 s by default).

- **Listen on `0.0.0.0`, not `localhost` or `127.0.0.1`.** A server bound to
  loopback is unreachable from outside the instance (`app_loopback_bound`).
- **Read the port from `PORT`.** Gregale sets it for every app. An app on a
  hard-coded port fails with `app_not_listening`, and the detail names both
  ports. To pin the port instead, set `hosting.port` in `gregale.yaml`; see
  [Deployment overrides](deploy-overrides.md).
- **Make the health path answer quickly.** If the source defines `/healthz`,
  `/health`, `/readyz`, `/ready`, `/livez` or `/live`, Gregale uses it as the
  health path, and it must return 2xx without calling slow dependencies. An app
  that rejects unknown hosts, such as Django with `ALLOWED_HOSTS`, must answer
  that path first; see [Deploy a Django app](deploy-django.md#health-check).
  Do not use `/` as the health path; see
  [Health checks](frameworks.md#health-checks).
- **Give a slow starter more time.** If the app legitimately needs longer, for
  example to load a model or warm a cache, raise the deadline with
  `gregale deploy --startup-deadline-s 60` or `lifecycle.startup_deadline_s`
  in `gregale.yaml`. The plan caps it: 30 s on Free and Hobby, 60 s on Pro,
  120 s on Scale.

## The app ran out of memory

**Code:** `app_runtime_oom`

The kernel stopped the app for using more memory than its size allows; the
detail carries the peak it reached. Raise the app's memory to at least that
peak plus 8 MB:

```bash
gregale app update my-api --ram 512
```

Your plan sets the per-app maximum; see [Plans and pricing](plans.md). Very
small sizes leave little room once the runtime itself is loaded, so a
framework with several worker processes, such as gunicorn, usually needs more
than 128 MB. Fewer workers also lowers the peak.

## The build failed

**Codes:** `build_failed`, `dep_install_failed`, `build_oom`

The end of the build output appears when `gregale deploy` stops, and
`gregale release logs` has the whole log.

- **Dependencies did not install.** Commit the lockfile
  (`package-lock.json`, `pnpm-lock.yaml`, `poetry.lock`, `uv.lock`) and pin
  versions, and check that a private package's registry credentials are set;
  see [Private registry credentials](registry-credentials.md). A failing
  `postinstall` script fails the build the same way.
- **A Dockerfile step failed.** The log names the step, for example
  `[build 3/5] RUN npm run build`. Run `docker build .` locally to reproduce
  it.
- **The build ran out of memory or time.** Each build gets 2 vCPU, 2048 MB
  and 15 minutes. Reduce bundler parallelism, for example
  `NODE_OPTIONS=--max-old-space-size=1536`, or build in CI and deploy the
  result. See [Build limits](build.md#limits).

## The app is too large

**Code:** `app_layer_too_large`

The built app layer, everything added on top of the runtime base, is capped
by plan: 256 MB on Free, 512 MB on Hobby, 1 GB on Pro and 2 GB on Scale. Use a
multi-stage Dockerfile, install production dependencies only, and keep build
inputs, test data and caches out of the final image. See
[App layer](build.md#app-layer).

## The release command failed

**Code:** `release_command_failed`

The release command (`release:` in a Procfile, or `release.command` in the
manifest), usually a migration, exited non-zero or timed out. The new version
never served, and the previous release is still live. Read the command's
output with `gregale release logs`, fix it and deploy again. Run the same
command locally against a copy of the database first when you can. See
[Releases that change the database](releases.md#releases-that-change-the-database).

`release_infrastructure_failed` means Gregale could not run the command or
read its result. Nothing in your code needs to change; deploy again.

## The new version broke production

A deploy that passed its checks can still misbehave. Go back to the release
that served before it, without rebuilding:

```bash
gregale rollback my-api            # the release that most recently stopped serving
gregale rollback my-api --to v41   # a specific release
```

If the bad release changed the database, see
[Rollbacks that restore the database](releases.md#rollbacks-that-restore-the-database).

## It is not your code

**Codes:** `capacity_unavailable`, `release_infrastructure_failed`,
`build_infrastructure_failed`, `deployment_verification_unavailable`

These mean Gregale could not finish the deploy, not that the app is wrong.
The previous release keeps serving. Wait a minute and deploy again; check
[status.gregale.dev](https://status.gregale.dev) if it keeps happening.

## Still stuck

Email [support@gregale.dev](mailto:support@gregale.dev) with the app name, the
deployment ID or version from `gregale release list`, and the error code. Do
not send secrets or credentials.
