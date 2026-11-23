# Troubleshooting

Find what you are seeing, then follow the steps under it. Each section names
the error `code` you are likely to get, so you can also search this page for
the code in a CLI message or an API response. The
[error code reference](error-codes.md) lists every code.

## Start here

Three commands answer most questions about an app:

```sh
gregale inspect my-api                 # the main problem and one next command
gregale logs my-api --latest-failure   # build log of the latest failed deployment
gregale logs my-api --since 1h         # what the app printed in the last hour
```

`gregale inspect` puts the most important known problem first and ends with a
command you can copy. For a failed deployment it includes the recorded cause
and recovery step. See [Deployments, settings and health](cli-inspect.md).

## A deploy failed

Check the source before uploading it again. Neither command changes anything
on Gregale:

```sh
gregale deploy --dry-run    # the detected framework, start command, port and health path
gregale deploy check        # checks the source or image for known problems
```

### Gregale could not tell how to build or start the app

Code: `build_undetected`.

Detection looks for `package.json`, `requirements.txt` or `pyproject.toml`,
`go.mod`, or a `Dockerfile`. Run `gregale scan --explain` to see why nothing
matched. Then add a `Dockerfile`, or set the start command in `gregale.yaml`:

```yaml
hosting:
  start: node server.js
  port: 8080
```

See [Builds and framework detection](build.md) and
[Deployment overrides](deploy-overrides.md).

### Dependencies failed to install

Code: `dep_install_failed`.

Read the build log with `gregale logs my-api --latest-failure`. Commit your
lockfile (for example `package-lock.json`, `poetry.lock`, `uv.lock` or
`go.sum`) so the build installs the versions you tested. A package from a
private registry needs its credentials; see
[Private registry credentials](registry-credentials.md). A failing
`postinstall` script fails the build the same way.

### A Dockerfile step failed

Code: `build_failed`.

The build log names the step, for example `[build 3/5] RUN npm run build`.
Run `docker build .` locally to reproduce it.

### The build ran out of memory or time

Codes: `build_oom`, `build_timeout`.

Each build gets 2 vCPU, 2048 MB of memory and 15 minutes. Lower the build's
parallelism (for Node, `NODE_OPTIONS=--max-old-space-size=1536` or fewer
bundler workers), or build in CI and deploy the result. See
[build limits](build.md#limits).

### The app is too large

Code: `app_layer_too_large`.

What your build adds on top of the runtime base is capped by plan: 256 MB on
Free, 512 MB on Hobby, 1 GB on Pro and 2 GB on Scale. Use a multi-stage
`Dockerfile`, leave development dependencies out of the final image, and keep
build inputs out of the final stage.

### The app built but never became ready

| Code | What happened | What to do |
|---|---|---|
| `app_not_listening` | Nothing answered on `$PORT`, or the app listens on another port. The message names both ports | Listen on the port in `$PORT`, or set `hosting.port` in `gregale.yaml` to the port the app uses. |
| `app_loopback_bound` | The app listens on `127.0.0.1` or `localhost`, which Gregale cannot reach | Listen on `0.0.0.0`. Development servers often default to localhost; use the production start command. |
| `app_startup_crash` | The process exited before it was ready. The message ends with its last output lines | Fix the error in those lines. `gregale logs my-api --latest-failure` has the full output. |
| `app_runtime_oom` | The app used more memory than its limit while starting. The message carries the peak | Raise the memory to at least the peak plus 8 MB with `gregale app my-api --ram <MB>`, or lower the app's memory use. Your plan sets the maximum per app ([plans](plans.md)). |
| `app_startup_timeout` | The app did not become ready in time | Start the server before slow work such as cache warm-up or migrations. Run `gregale deploy check`. |
| `app_healthz_unauthorized` | The health path answered 401 | Make the health path answer without credentials, or set `hosting.health` to a path that does. |

The most common causes behind those codes:

- **A missing environment variable or secret.** The app crashes reading a
  variable that is not set. Add it with `gregale env set --app my-api NAME=value`,
  or for a credential see [Secrets](secrets.md), then deploy again.
- **The process exits when it is done.** A web app must keep running. A start
  command that runs a script and exits with code 0 still fails, because
  nothing is left to serve. For run-to-completion work, use a [job](jobs.md)
  or a [cron](crons.md).
- **The health path is slow or guarded.** When the source defines `/healthz`,
  `/health`, `/readyz`, `/ready`, `/livez` or `/live`, Gregale waits for it to
  answer 2xx, so keep it free of slow dependencies. An app that rejects
  unknown hosts, such as Django with `ALLOWED_HOSTS`, must answer that path
  first; see [Deploy a Django app](deploy-django.md#health-check). Do not use
  `/` as the health path; see [Health checks](frameworks.md#health-checks).
- **The app needs longer to start.** The startup deadline is 15 seconds by
  default on Free and 30 on the other plans. Set `lifecycle.startup_deadline_s`
  in `gregale.yaml` for an app that loads a model or warms a cache first; a
  deploy from a local directory can also pass `--startup-deadline-s`. The plan
  caps it: 30 seconds on Free and Hobby, 60 on Pro and 120 on Scale.
- **The memory size is too small for the framework.** Very small sizes leave
  little room once the runtime is loaded, so a server with several worker
  processes, such as gunicorn, usually needs more than 128 MB. Fewer workers
  also lower the peak.

### The release command failed

Code: `release_command_failed`.

The release command (`release:` in a Procfile, or `release.command` in the
manifest), usually a migration, exited non-zero or timed out, so the new
version never served. Read its output with
`gregale release logs v42 --app my-api`, using the failed version, then fix it
and deploy again. Run the same command against a copy of the database
first when you can. See
[Releases that change the database](releases.md#releases-that-change-the-database).

### Gregale could not finish the deploy

Codes: `capacity_unavailable`, `release_infrastructure_failed`,
`build_infrastructure_failed`, `deployment_verification_unavailable`.

These mean the platform could not finish the deploy, not that the app is
wrong. Wait a minute and deploy again; check
[status.gregale.dev](https://status.gregale.dev) if it keeps happening.

When a deployment fails, the release that was serving keeps serving. Nothing
changes for your users until a new deployment succeeds.

## The new version broke production

A deploy that passed its checks can still misbehave. Go back to the release
that served before it, without rebuilding:

```sh
gregale rollback my-api            # the release that most recently stopped serving
gregale rollback my-api --to v41   # a specific release
```

If a release command, such as a migration, ran after the release you are
going back to last served, the rollback is refused with
`rollback_crosses_release`: the older code would run against the changed
database. Pass `--acknowledge-releases` to go back anyway, or
`--restore-databases` to restore the managed database with it. See
[Rollbacks that restore the database](releases.md#rollbacks-that-restore-the-database).

## Requests fail

Gregale sends its own failures as JSON with a `code` and the same value in the
`X-Faas-Error-Code` header. Look for the code first; the HTTP status alone
does not tell you which side failed.

### 503 with a code

| Code | Meaning | What to do |
|---|---|---|
| `app_unavailable`, `app_health_unavailable`, `bad_gateway` | Gregale could not reach a running instance of the app | Retry after the delay in `Retry-After`. If it keeps happening, read `gregale logs my-api --since 15m` for crashes. |
| `request_budget_exceeded` | The app did not send response headers within the request budget, 30 seconds by default | Send headers sooner: stream the response, or accept the request and finish the work in the background. |

`capacity_unavailable` means Gregale cannot take on more work at the moment;
retry with backoff.

### A plain `error code: 502` or `error code: 504` page

The app itself returned 502 or 504. Cloudflare, in front of Gregale, replaces
those response bodies with its own page. Find the cause in
`gregale logs my-api`. If your clients need a JSON body for a temporary
failure, return 503 with your own error body instead. See
[Errors](errors.md#502-503-and-504-responses).

### 402, 403 or 429

| Code | Meaning | What to do |
|---|---|---|
| `ip_denied`, `geo_denied`, `waf_blocked` | An edge rule on the app refused the request | List the rules with `gregale edge rule list --app my-api`. See [Edge rules](edge-rules.md). |
| `api_rate_limited` | Not your app's traffic: your account sent more Gregale API or CLI requests this minute than its plan allows | Wait for `Retry-After` and spread requests out. |
| `plan_*` (often 402) | The plan does not include the feature or quota | Check [Plans and pricing](plans.md). |

## The first request is slow

An app with no traffic scales to zero, and the next request wakes it. The
first request pays the wake time; the ones after it run at normal speed. See
[How scaling to zero works](cold-wake.md).

- **Keep it awake for longer.** `gregale app my-api --idle 120` parks the app
  after 2 minutes without requests. The most you can set is twice your plan's
  default: 2 minutes on Free and Hobby, 10 on Pro and 20 on Scale.
- **Keep instances always running** (Pro and Scale): `gregale app my-api --min 1`.
  A warm instance is billed as always running.
- **Make the wake itself faster.** Do slow setup once at startup rather than
  on the first request, and refresh connections after a wake with the
  [restore hooks](cold-wake.md#refresh-application-state-after-restore).

## A custom hostname is not serving

```sh
gregale domain doctor api.example.com
```

`domain doctor` checks ownership, routing and HTTPS without changing anything,
and says what to fix. The usual causes:

- **Ownership is not proven yet.** The `_faas-verify` TXT entry Gregale printed
  when you added the hostname is missing; `gregale domain get` shows it again.
- **A proxy answers for the name.** Point the name straight at Gregale. A
  proxied (orange-cloud) Cloudflare entry stops Gregale from getting a
  certificate.
- **CAA.** If your zone has CAA entries, they must allow `letsencrypt.org`.
- **The status is `dns_drifted`.** An entry was removed or changed after setup.
  Put it back.

The full setup steps are in [Domains](domains.md#verification).

## Logs are missing

- **The app is parked.** Its lines move to the log archive when the instance
  stops, and can be read there after about five minutes.
  `gregale logs my-api --since 3h` covers every instance from the last three
  hours.
- **You are looking for build output.** It is kept separately:
  `gregale logs my-api --latest-failure`.
- **The lines are older than your plan keeps.** See
  [log retention](logs.md#retention).

## Still stuck

Email support@gregale.dev with the app name and the request or deployment ID
from the error. Never include secrets. See
[Reporting an error](errors.md#reporting-an-error).
