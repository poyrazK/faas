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
lockfile (for example `package-lock.json` or `go.sum`) so the build installs
the versions you tested.

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

When a deployment fails, the release that was serving keeps serving. Nothing
changes for your users until a new deployment succeeds.

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

## A custom domain does not work

```sh
gregale domain verify api.example.com --wait --timeout 10m
gregale domain doctor api.example.com
```

`domain verify` reports ownership, routing and HTTPS separately and tells you
which DNS record to change. The usual causes:

- **The TXT record is missing.** Ownership needs the `_faas-verify` TXT record
  that Gregale printed when you added the domain (`gregale domain get` shows it
  again).
- **A proxy answers for the name.** Point the name straight at Gregale. A
  proxied record, such as an orange-cloud record on Cloudflare, stops Gregale
  from getting a certificate.
- **CAA records.** If your zone has CAA records, they must allow
  `letsencrypt.org`.
- **The domain shows `dns_drifted`.** A DNS record was removed or changed after
  verification. Put both records back and verify again.

See [Domains](domains.md#verification).

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
