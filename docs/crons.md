# Crons

A cron sends an HTTP `POST` to a path on your app on a schedule. The app does
not have to be running: Gregale wakes it for the request, exactly as it does
for visitor traffic, and lets it scale back to zero afterwards.

Crons are available on Hobby, Pro and Scale. Free accounts get
`402 plan_crons_not_allowed`.

| Plan | Crons per app | Crons per account |
|---|---|---|
| Hobby | 5 | 10 |
| Pro | 20 | 50 |
| Scale | 100 | 500 |

Disabled crons count toward both limits. A request over a limit gets
`403 plan_cron_quota` with the limit and your current count.

## Create a cron

```bash
gregale cron create --app my-api --schedule "0 3 * * *" --path /jobs/nightly
gregale cron create --app my-api --schedule "*/15 * * * *" --path /jobs/sync --timezone Europe/Istanbul
gregale cron create --app my-api --interactive   # pick a path, preview run times, confirm
```

`--schedule` takes the standard five-field expression (minute, hour, day of
month, month, day of week). The finest granularity is one minute. A schedule
that can never fire, such as `0 0 30 2 *`, is rejected.

`--path` is the request path on your app and defaults to `/`. It starts with a
single `/`, may carry a query string, and is at most 2,048 bytes; whitespace,
control characters, backslashes and `#` are rejected. A cron always calls its
own app, so a path cannot name another host.

`--timezone` takes an IANA name and defaults to `UTC`. Schedules follow the
zone's daylight-saving rules: a time that falls in a spring-forward gap fires
once at the first valid minute after it, and a fixed time that repeats when
clocks fall back fires once. Interval schedules such as `*/30 * * * *` keep
following the wall clock.

Preview upcoming run times with `gregale cron next --app my-api`.

Manage crons with `gregale cron list --app my-api`, `gregale cron get ID`,
`gregale cron update ID`, and `gregale cron delete ID`. The same operations are
available on `/v1/crons` in the API and as the `gregale_cron` Terraform
resource.

### Declare crons in gregale.yaml

```yaml
triggers:
  - kind: cron
    app: my-api
    schedule: "0 3 * * *"
    path: /jobs/nightly
    timezone: Europe/Istanbul   # optional
    skip_if_running: true       # optional
```

When `triggers:` names an app, that app's HTTP crons match the file exactly
after each deploy: crons missing from the file are removed. An empty
`triggers: []` removes them all. If the file has no `triggers:` key, or none
of its cron triggers name the app, the app's crons are left alone.

## What your app receives

Each run is a `POST` to the cron's path with an empty body and these headers:

| Header | Value |
|---|---|
| `X-Faas-Invocation-Source` | `cron` |
| `X-Faas-Invocation-Id` | the run's id, as shown by `gregale cron runs` |

Gregale removes any `X-Faas-*` header a visitor sends, so a request carrying
`X-Faas-Invocation-Source: cron` came from your cron. The cron's path is still
an ordinary route that anyone can call. If running the job twice is harmful,
check that header, or use the invocation id to ignore a run you have already
done.

The handler has 30 seconds to answer, or the app's request timeout when it is
longer and your plan allows it. A run that takes longer is recorded as a
timeout. Work that needs more time belongs in a [job](jobs).

## Delivery

Each scheduled time produces one run, and Gregale makes one attempt at it. A
run fails when the app answers with an error status, times out, or cannot be
woken. A failed run is not retried; the next scheduled time is the next
attempt. To retry by status code or outcome, set failure rules (see
[Advanced scheduling](#advanced-scheduling)).

Runs are delivered by the same queue as other background work. A run normally
starts within a few seconds of its scheduled minute; it can start later when
your account already has many background invocations in flight, but it is not
dropped.

`--skip-if-running` skips a scheduled time while the previous run of the same
cron is still queued or running.

If Gregale could not reach a scheduled time at all, for example during
maintenance, the cron runs once as soon as it can. It does not replay every
time it missed.

### Schedule changes take effect from the next time

Changing a cron's schedule or timezone, enabling a disabled cron, or a
redeploy that lifts a suspension (see below) starts the schedule again from
that moment. The cron next runs at the first scheduled time after the change.
It never runs immediately because of the change, and it does not catch up on
the times it passed while it was off.

Disabling a cron stops future runs. It does not cancel a run that is already
queued or in progress.

### Suspension

If an app has no live deployment when its cron is due, the cron is suspended
with `suspended_reason: no_live_deployment` instead of failing on every
schedule. The next successful deploy lifts the suspension; a cron you disabled
yourself stays disabled. Deleting an app suspends its crons with
`app_deleted`, and restoring the app brings them back.

## Run a cron now

```bash
gregale cron run CRON_ID
gregale cron run --app my-api --interactive      # pick a cron and follow the run
gregale cron fire-now REQUEST_ID --wait          # follow a request you already made
```

A manual run sends the same request as a scheduled run and does not move the
schedule. The request goes `pending`, then `running` while the run is queued
and delivered, then `succeeded` or `failed` with the run's error. A cron can
have five manual runs waiting or running at once; another request gets
`429 cron_fire_now_busy` until one finishes.

## History and alerts

```bash
gregale cron runs CRON_ID                 # newest first
gregale cron runs CRON_ID --before CURSOR # older runs
```

Each run shows when it started, how long it took, its outcome (`success`,
`failed`, `timeout`) and the error. Runs are kept for the
[invocation history retention](event-driven#invocation-history-retention)
period.

Schedule decisions (`gregale crons occurrences CRON_ID`) and manual-run
requests (`gregale crons fire-now REQUEST_ID`) are kept for 90 days. A
decision that is still waiting or running is kept until it settles.

To be told when runs fail, add an alert rule on failed cron invocations:

```bash
gregale alert create --app my-api --name "cron failures" --metric failed_invocations \
  --failure-source cron --comparison gte --threshold 1 --window-spec 1h --channel OPS_SLACK_ID
```

## Billing

A cron run is billed like any other request: the app's plan RAM for every
second it runs, including the wake. A cron that fires every minute keeps a
small app warm most of the time, so prefer the longest interval the job allows.

## Advanced scheduling

`--schedule-policy` and `--failure-rules` take versioned JSON for crons that
need more control:

- **Overlap**: `allow`, `skip` or `replace` a run that is still active.
- **Missed runs**: `skip` or `coalesce_latest` the times a busy or unavailable
  scheduler passed, with each decision recorded and listed by
  `gregale cron occurrences CRON_ID`.
- **Start deadline**: give up on a run that cannot start within N seconds.
- **Failure rules**: map HTTP statuses or a reported `outcome_code` to
  `retry` or `fail_partition` (fail without retrying), and choose whether an
  unknown outcome is held or retried. Retries use your plan's retry budget,
  so a cron with failure rules can run more than once for one scheduled time;
  make such handlers idempotent.

## Command crons (preview)

A command cron runs a command from the app's live deployment instead of
sending an HTTP request. Command crons are built on [app tasks](apps), which
are in preview and not enabled on every host; where they are off, creating
one returns `501 not_implemented`.

Use a command cron when a recurring task needs the app's deployment
environment without an HTTP endpoint:

```bash
gregale cron create --app APP_ID --schedule "0 2 * * *" \
  --command bin/rebuild-index --arg=--incremental --timezone Europe/Istanbul
gregale cron create --app APP_ID --schedule "0 4 * * 0" \
  --command "bin/cleanup --older-than 30d" --shell
gregale cron runs CRON_ID
```

Command crons create one deployment-attached app task for each scheduled
occurrence and select the app's currently live deployment at fire time, so a
later deployment automatically supplies the new command environment. The
command runs with a 10-minute timeout and 1 MiB output limit by default; use
`--timeout-seconds` and `--max-output-bytes` to adjust them. `--arg` is
repeatable and preserves argument boundaries. `--shell` instead treats the
single `--command` value as a shell string and cannot be combined with `--arg`.
Use `--skip-if-running` to skip a firing while an earlier command task remains
active. By default command failures are not retried. Set `--retry-max` to allow
up to five additional attempts after a command fails or times out; retries use
exponential backoff from `--retry-backoff-seconds` (default 60 seconds, capped
at 24 hours). A cron run remains one logical history row while it waits for a
retry, and its task receipt reports the attempt count and next retry time.
Failure rules can classify an exit code or a structured `outcome_code` written
to `GREGALE_OUTPUT_MANIFEST_PATH`; a mapped code can turn exit 0 into a
permanent failure or a retry. Unmapped successful outcomes remain successful.
Retries are at-least-once: a command may have produced side effects before it
failed, so make retryable commands idempotent. A worker lease lost after
dispatch is not automatically replayed because completion is uncertain.

To report a business outcome independently of the process exit status, write
a version-1 result manifest atomically before the command exits:

```bash
printf '%s\n' '{"version":1,"artifacts":[],"outcome_code":"invalid_record"}' \
  > "$GREGALE_OUTPUT_MANIFEST_PATH.tmp"
mv "$GREGALE_OUTPUT_MANIFEST_PATH.tmp" "$GREGALE_OUTPUT_MANIFEST_PATH"
```

Configure that code explicitly with `--failure-rules`; Gregale does not infer
retry safety from an error message. The command receipt retains the code and
classifier decision for inspection.

Inspect outcomes with `gregale cron runs CRON_ID`. The history includes a run
id; for a command cron, inspect its captured stdout/stderr, exit status, and
retry details on demand with `gregale cron runs CRON_ID --run TASK_ID`. Use
`gregale cron run CRON_ID` to immediately run the cron's saved command on the
current live deployment without moving its schedule cursor.
If `--skip-if-running` is configured and another run is still active, the
manual request fails rather than overlapping it.
Cancel a queued or active command-cron run with
`gregale cron cancel CRON_ID TASK_ID`; an active task reports its cancellation
request while the worker stops it. Disabling a cron only prevents future fires.
`gregale app APP_ID exec ...` remains the surface for an arbitrary one-off
command.
