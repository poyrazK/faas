# gregale CLI — shell completion + man pages setup

The gregale CLI emits shell completion scripts and man pages from the
binary itself. No checked-in copies in `contrib/completion/`; the
binary is the source of truth. This doc covers the install path for
each shell, plus the man-page install for offline / CI use.

## Selecting an app

`gregale logs`, `gregale logs tail`, `gregale inspect`, and `gregale open`
use an explicit app slug first, then the app saved by `gregale link`.
When neither identifies an app, an interactive terminal offers an app picker.
Type search text to filter by app slug, project, or status, then enter a
displayed number or exact slug. `/` clears the filter; `q` or Ctrl-C cancels.
An empty answer does not select an app.

The picker stays within the linked project when one exists. It shows project
membership and status, and labels any saved environment as the linked
environment. These app commands do not select an environment deployment.
Selection applies only to this invocation and does not change the saved link.

JSON output, `--non-interactive`, and redirected input or output never prompt.
For scripts, pass the slug explicitly or save a specific app with
`gregale link <project-slug> --app <slug>`. `gregale open` also accepts
`--app <slug>`.

## Guided resource settings

Run `gregale app my-api scale --interactive` to review current settings and
choose a resource profile, instance cap, and minimum warm instances within
your account plan's limits. Add `--environment staging` to edit that
environment's desired workload settings.

The flow previews the changes, compatibility warnings, and available resident
usage estimates. Choose to finish, save a reusable plan to a new file, or apply
after a separate confirmation (default: no). Applying reuses saved-plan checks
and rejects settings that changed since the preview was read.

This mode requires terminal input and output and cannot be combined with JSON,
automation mode, setting flags, or the saved-plan flags. For scripts, use
`scale --plan ... --out plan.json`, then `scale --apply plan.json --confirm`.

## Guided secret entry

Run `gregale secrets set --app my-api --interactive` to enter secret names
and hidden, single-line values directly. Choose a scope (the linked environment
is the default) and retention: preserve existing classes, persistent, or
ephemeral. Ephemeral secrets disable VM snapshots for their scope.

Review the app, scope, retention, and names before confirming. Values are kept
out of the review and shell arguments. Empty names finish entry; Ctrl-C or
Ctrl-D cancels before saving. Duplicate names and values exceeding the account
plan's byte limit are rejected during entry. Keys are saved individually, so
an error may leave earlier keys saved. After a successful save, the flow offers
an optional fresh restart, defaulting to no.

The flow requires terminal input and output. It cannot be combined with JSON,
automation mode, positional values, `--from-stdin`, or `--restart`. For scripts,
use the existing `--from-stdin` option.

## Guided rollback

Run `gregale rollback my-api --interactive` to choose a historical release
from the app's deployment history. The list shows revision (or deployment ID),
creation date, status, and scope. The flow identifies a completed release
serving full traffic in that same scope, then shows the selected release's
summary. Summary changes compare that release with its own predecessor.

Enter an optional reason and confirm (default: no). The request pins both the
selected target and the observed current deployment. The checked rollback API
rechecks artifact availability, deployment state, bindings, and handoff
requirements; a changed current deployment requires a fresh review. The flow
waits for completion and uses the existing operation report to show blockers.
`--timeout` and `--poll-interval` control the wait. Interruption or a wait timeout
does not undo an accepted rollback; use its operation ID with
`gregale rollback status my-api --operation <id> --wait` to continue observing.

This mode requires terminal input and output. For scripts or JSON output,
use explicit `--to` and `--expected-current` flags. The guided mode accepts
only `--timeout` and `--poll-interval` alongside `--interactive`.

## Guided custom-domain setup

Run `gregale domains setup api.example.com --app my-api` to bind a domain,
show the DNS records to publish, and wait for ownership verification and an
unexpired TLS certificate. Add `--environment staging` for an environment
binding. An existing binding is reused only when its app and environment match.

After publishing the records, confirm when to start checking. Pending results
include available DNS and certificate diagnostics. The wait defaults to 10
minutes; `--timeout` accepts up to 1 hour, and `--poll-interval` accepts 5 seconds
to 1 minute. Timeout returns a failure status and prints a command to continue
checking the same binding. Leaving the flow preserves the binding and records.
For app bindings, the flow offers a default-domain change with a separate
confirmation, defaulting to no. Environment bindings keep their environment routing.

Setup requires terminal input and output. For scripts or JSON output, use the
existing `domains add`, `domains verify`, and `domains set-default` commands.

## Guided connection switching

Run `gregale profile use --interactive` to choose a saved connection. The list
shows each API address and marks the active profile. The flow checks the chosen
endpoint with that profile's stored credential and displays the account identity
before asking to switch (default: no). Failed checks leave the active profile
unchanged. Use `--timeout 5s` to adjust the connection check deadline.

Run without a prefix `--profile` flag or `FAAS_API` / `FAAS_TOKEN` overrides so
the check reflects the saved endpoint and its own credential. Terminal input
and output are required. Scripts can use `gregale profile use <name>` and
`gregale profile check` separately.

## Guided environment promotion

Run `gregale projects environments promote my-project --interactive` to choose
source and destination environments. Protected environments are marked in the
list. Choose whether to copy non-secret source configuration; secrets remain
scoped to the destination.

The flow displays the existing promotion preview, including release graphs,
workload changes, configuration changes, and blockers. Confirmation names the
project and both environments and defaults to no. Promotion uses the exact
reviewed preview token and the existing protected-environment approval flow.
Stale previews are rejected by the server and require a fresh review.

After submission, the flow waits and shows promotion progress. Use `--timeout`
to set the wait deadline (seconds or a duration, up to 24 hours). Interruption
or a wait timeout does not undo an accepted promotion. The existing promotion
status command can continue observing its promotion ID.

This mode requires terminal input and output and accepts only `--timeout`
alongside `--interactive`. Scripts retain the existing explicit `--from`,
`--to`, `--sync-config`, `--yes`, `--wait`, and `--progress` options.

## Guided log filtering

Run `gregale logs my-api --interactive` to choose runtime output or HTTP
request events, a time window, and filters. Runtime queries offer log level,
text matching, and optional following. HTTP queries offer an exact status code
and route path and return up to 100 events.

The flow prints an equivalent command for POSIX shells, then asks whether to
run it. Declining leaves the command available to copy. App selection uses the
explicit target, linked app, or the interactive app picker. `--app my-api` is
also accepted.

This mode requires terminal input and output and accepts only an app target
alongside `--interactive`. Scripts and JSON output retain the existing source,
time-window, and filter flags.

## Saved log views

Save frequently used filters and reuse them for any explicit or linked app:

```sh
gregale logs views save runtime-errors --since 1h --level error
gregale logs my-api --view runtime-errors
gregale logs --view runtime-errors
gregale logs views list
gregale logs views show runtime-errors
gregale logs views delete runtime-errors
```

The guided `logs --interactive` flow also offers to save the chosen filters.
Names use 1–64 lowercase letters, digits, underscores, or hyphens. Use
`--replace` when saving over an existing name. Views require relative windows
such as `15m`, `1h`, or `3d`, recalculated each time you run them.

Views store filter text in `log-views.json` beside the CLI configuration, with
file permissions restricted to the owner. They do not store app targets,
credentials, or log output. Runtime views support level, text, and follow;
HTTP views support status, route, and page size. Supply only an app target
alongside `--view`; inspect or replace the saved view to change its filters.

## Guided project linking

```sh
gregale link --interactive
# Skip the project chooser when you know the project:
gregale link my-project --interactive
```

Choose a project, default app, and default environment. The flow shows current
checkout defaults and the proposed replacement, marks protected environments,
and asks before saving. You can choose no default app or environment. Selecting
an environment sets the scope used by commands that support linked environment
defaults; protection requirements still apply to future operations.

The guide saves the existing local project context and adds `.gregale/` to the
repository `.gitignore` if needed. Pass `--no-gitignore` to skip that update.
It requires an interactive terminal; for scripts, use
`gregale link PROJECT --app APP --environment ENV`. Run `gregale context` to
inspect saved defaults or `gregale unlink` to remove them.

## Guided secret removal

```sh
gregale secrets unset --app my-api --interactive
gregale secrets unset --app my-api --scope staging --interactive
```

The guide lists secret names only in the selected scope, using the linked
environment when `--scope` is omitted (otherwise `default`). Choose a key,
choose whether to request a fresh app restart and wait up to two minutes for
runtime removal acknowledgement, then review the app, scope, and key before
confirming. It rechecks the selected secret before removal and stops if the
secret's update metadata has changed.

Without a restart, running instances retain their boot environment until the
next cold wake. A restart affects the app. If restart or acknowledgement fails
after removal, the removal has already happened; the command reports that
outcome. The guide requires an interactive terminal. Scripts can continue to
use `gregale secrets unset --app APP KEY --scope SCOPE` with `--restart`,
`--wait-for-ack`, and `--timeout` as needed.

## Preview an environment upload

```sh
gregale env push --app my-api --dry-run
gregale env push --app my-api --scope staging -f .env.staging --dry-run --restart
```

The preview uses the same input parsing and secret scan mode as `env push`.
It shows the resolved app and scope, keys to add or update, scan findings,
projected app secret usage across all scopes, and how changes would apply.
Values and scan snippets are hidden in both human and `--json` output.
Existing keys are marked as updates; sealed values cannot be compared to detect
unchanged values. Repeated keys are shown in upload order.

The default scan skips detected pairs; strict mode blocks the upload. Invalid
keys, values above the plan limit, exceeding the secret quota, or an input
entirely skipped by the scan produce a nonzero exit status. The JSON preview
includes `can_push`, `changes`, `scan_findings`, and `blockers`.

`--dry-run` reads account and secret metadata but uploads nothing and requests
no restart, even with `--restart`. Remove `--dry-run` to upload. The preview
reflects current metadata; the server validates rules again during the upload.
The `--from-stdin` input option is also supported.

## Browse deployment summaries

```sh
gregale deployment summary --app my-api --interactive
# Use the linked app, or choose an app when no default is linked:
gregale deployment summary --interactive
```

Choose a release from recent app history, with revision, status, creation time,
and scope shown for each entry. Choose **Show older releases** to browse the
next page or **Cancel** to exit. The summary shows changes relative to the
preceding release and the reported rollback target. The flow prints an
equivalent command pinned to the selected release ID for reuse.

The guide requires an interactive terminal. For scripts or JSON output, use
`gregale deployment summary ID --app APP --json`. The history picker reads
release data and does not submit a rollback.

## Install shell completion

```sh
gregale completion install --interactive
```

Choose Bash, Zsh, Fish, or PowerShell. The default choice is inferred from
`SHELL` or the platform; you can select a different shell. Review the completion
script and startup file paths, then confirm installation. Bash defaults to
`.bashrc` (`.bash_profile` on macOS); Zsh respects `ZDOTDIR`; Fish uses its
user completion directory under `XDG_CONFIG_HOME` or `~/.config`. For PowerShell,
run `$PROFILE` in the host you use and enter that absolute path when prompted.
You can choose a different startup file for Bash and Zsh too.

The installer writes a generated completion script and a managed startup block
for shells that require it. Existing startup content is preserved and backed up
before changes. Reinstalling replaces the managed block without adding another
copy. Existing completion scripts without Gregale's installer marker are left
for manual setup. Open a new shell session to activate completion, and keep
`gregale` on `PATH`. Rerun installation after upgrading to refresh the script.

The installer requires an interactive terminal. Manual setup remains available
through `gregale completion bash`, `zsh`, `fish`, and `powershell`.

## Create a scheduled HTTP task

```sh
gregale crons add --app my-api --interactive
# Use the linked app, or choose one:
gregale crons add --interactive
```

Choose an app-relative request path, an explicit IANA timezone (default UTC),
and a schedule: every five minutes, hourly, daily at 09:00, weekdays at 09:00,
or a custom five-field cron expression. The guide previews the next three
scheduled times using Gregale's cron grammar and daylight-saving behavior,
then shows the app, path, expression, timezone, and enabled state before
asking to create the task. Actual execution may occur later than the scheduled
time. An equivalent command is printed for reuse.

Only `--app` can be supplied alongside `--interactive`; choose the other
settings in the flow. The guide creates enabled HTTP tasks with default policy
settings. Deployment command tasks and advanced policies continue to use the
explicit `crons add` flags. Scripts can use, for example,
`gregale crons add --app my-api --schedule '0 * * * *' --path /tasks/hourly --timezone UTC`.

## Edit a scheduled HTTP task

```sh
gregale crons update --app my-api --interactive
# Use the linked app, or choose one:
gregale crons update --interactive
```

Choose an existing HTTP task. The guide displays its current configuration,
prefills the path, timezone, and five-field schedule, and lets you enable or
disable it. Enter keeps the displayed value. Review only the proposed changes
and the next three expression times before confirming. Disabled or suspended
tasks do not run merely because expression times are shown; advanced policies
may also skip occurrences or delay execution.

Only changed fields are submitted. Existing overlap settings, schedule policies,
and failure rules are preserved. Keeping all settings unchanged exits without
an update. The guide rechecks configuration before saving and stops if it changed
during review. An equivalent command is printed for reuse. `--app` is accepted
only in interactive mode; scripts and advanced settings continue to use
`gregale crons update ID` with explicit update flags.

## Browse scheduled-task history

```sh
gregale crons runs --app my-api --interactive
# Use the linked app, or choose one:
gregale crons runs --interactive
```

Choose an HTTP or command task, then select a recent run to inspect its outcome,
duration, and failure text. Command runs also show captured stdout/stderr,
retry information, and output truncation when available. The guide returns to
the run picker after displaying details. Choose **Show older runs** to browse
the next page or **Done** to exit. An equivalent command is printed for each
page and selected command run.

The history API returns ten rows per page without an explicit next cursor.
A full page offers older runs; the next page may be empty. The browser detects
repeated cursors and bounds page traversal. Only `--app` can accompany
`--interactive`; for scripts or JSON, use `gregale crons runs ID` with
`--before`, `--limit`, or `--run` as appropriate. The guide reads history without
firing or canceling tasks.

## View upcoming scheduled tasks

```sh
gregale crons next --app my-api
gregale crons next --app my-api --json
# Use the linked app, or choose one in a terminal:
gregale crons next
```

The overview lists HTTP and command tasks, sorted by their next expression time.
Each row includes the task ID, state, timezone, kind, and target. Times use the
task's timezone and include the UTC offset; sorting compares actual instants.
Disabled and suspended tasks remain visible after active tasks, without a next
time. Schedule policies and overlap controls are labelled when present.

Expression times are candidates, not execution guarantees. Overlap decisions,
start deadlines, missed-run policies, and execution delays may skip or delay a
run. The command uses Gregale's shared cron grammar and daylight-saving behavior
and reads task metadata without firing or updating tasks.

JSON contains `app_slug`, `as_of`, and `items`, including `next_expression_at`,
state, policy metadata, and execution notes. Invalid schedule metadata is shown
per task and produces a nonzero exit status while retaining other results.
Tasks using host-local `Local` timezone need an explicit IANA timezone for a
reliable preview. Scripts need an explicit or linked app and never prompt.

## Run a scheduled task now

```sh
gregale crons run --app my-api --interactive
gregale crons run --app my-api --interactive --timeout 5m
```

Choose an HTTP or command task and review its current path or command, schedule,
and state before confirming one manual fire-now request. The guide rechecks
configuration before submitting, then follows the returned request for up to
two minutes by default. It prints commands to inspect that exact request,
browse task history, and inspect the resulting command run when available.
The active connection profile is included in these commands.

Following uses read requests and never submits another manual run. Timeout
exits with status 3; Ctrl-C stops following without cancelling the submitted
request. Use the printed `crons fire-now REQUEST_ID` command to inspect its
current status. A completed fire-now request is distinct from the resulting
run's execution details. Server admission rules still apply, including the
current task state and plan restrictions.

The guide requires an interactive terminal. Scripts can continue using
`gregale crons run ID`, then `gregale crons fire-now REQUEST_ID`. The `--app`
and `--timeout` flags apply only to interactive mode.

## Resume following a manual task request

```sh
gregale crons fire-now REQUEST_ID --wait --timeout 5m
gregale crons fire-now REQUEST_ID --wait --json
```

Follow an existing fire-now request without submitting another run. The command
shows status changes and prints history/detail commands when the request reaches
a terminal state. Without `--wait`, the existing single-status read remains
available. Waiting defaults to two minutes; `--timeout` requires `--wait` and
a positive duration.

Exit statuses are 0 for a succeeded fire-now request, 1 for failed/cancelled
requests or read errors, 3 for timeout, and 130 for interruption. Timeout and
Ctrl-C stop local following without cancelling the request; the printed resume
command continues reading that same request. Completion of the request remains
distinct from the resulting run's execution details.

`--wait --json` emits one receipt with `request_id`, `wait_result`, the latest
`request` when available, `resume_command`, and an `error` when following fails.
It also returns the appropriate exit status, so scripts can handle unfinished
requests without parsing human progress messages.

## Set up an alert preset

```sh
gregale alerts preset enable --app my-api --interactive
# Use the linked app, or choose one:
gregale alerts preset enable --interactive
```

Choose an enabled catalog preset and review its metric, comparison, threshold,
time window, default cooldown, and minimum plan. Enter an HTTPS webhook receiver
URL and a signing secret with hidden input, then review the app and rule before
confirming creation. The signing secret is neither printed nor placed in command
arguments. The guide creates a new enabled webhook alert using preset defaults
and rechecks the selected catalog entry before creating it. Server plan and
admission checks still apply.

Only `--app` can accompany `--interactive`. For cooldown overrides, disabled
rules, other actions, or scripts, use the explicit preset flags with
`--webhook-secret-stdin`. Guided setup requires terminal input and output.
After creation, it prints a command to inspect the rule.

## Inspect alert deliveries interactively

Choose an alert rule without copying its ID:

```sh
gregale alerts deliveries --app my-api --interactive
# Use the linked app, or pick an app when none is linked:
gregale alerts deliveries --interactive
```

The flow shows the selected rule, asks whether to include test deliveries, and
lets you choose how many recent deliveries to fetch (1–100, default 20).
The table shows delivery status, attempts, HTTP response code, observed value,
and errors. When test deliveries are included, a `TEST` column identifies them.
This flow only reads delivery history; it does not send a webhook.

A reusable command is printed with the selected rule ID and active profile.
For scripts, use the explicit command with JSON output:

```sh
gregale alerts deliveries ALERT_ID --app my-api --limit 20 --json
```

## Edit alert settings interactively

```sh
gregale alerts update --app my-api --interactive
# Use the linked app, or choose one:
gregale alerts update --interactive
```

Choose a rule by name, then edit its prefilled threshold, window, cooldown,
and enabled state. The metric and action are shown for context. Review the
changed values and confirm before saving; unchanged values are omitted from
the update. Keeping all values unchanged makes no update request.

The CLI rereads the rule before saving and stops if its configuration changed
while you were editing. This is a client-side check, not an atomic server-side
lock. Routine evaluation state and timestamps do not block an edit.
For scripts or other fields, use `alerts update ALERT_ID --app APP` with
explicit flags.

## Inspect automatic rollback actions interactively

```sh
gregale alerts actions --app my-api --interactive
# Use the linked app, or pick one:
gregale alerts actions --interactive
```

Pick an action from the returned history, ordered newest first and labeled with
its status, rule ID, and fire time. The CLI fetches fresh details for that action
and shows its deployment pair, evidence, blockers, and rollback or service
handoff progress. It prints a reusable inspection command with the active profile.

For a pending or blocked action, choose whether to follow it until completion.
The wait defaults to 10 minutes with a 2-second polling interval; configure these
with `--timeout` and `--poll-interval`. A resume command is printed before waiting.
Interrupting or timing out stops observation; the rollback continues independently.
The flow only reads status and never submits or retries rollback actions.

For scripts, use `alerts actions --app APP --fire UUID [--wait] --json`.

## Check binding readiness interactively

```sh
gregale bindings check --app my-api --interactive
# Use the linked app, or choose one:
gregale bindings check --interactive
```

Choose a live deployment from paginated history. The check stays pinned to its
ID and scope, including live candidates with no traffic. Choose the maximum age
of verification evidence (default 10 minutes), whether to require current
PostgreSQL/object-storage application acknowledgements, and whether to waive
unsupported connectivity coverage for active queue/outbound bindings (default no).

The flow prints a reusable command and the readiness report, including blockers
and runtime freshness. If blocked, optionally wait with the existing read-only
poller. It stops when checks pass or blockers cannot progress through existing
work. Configure the wait with `--timeout` and `--poll-interval`; a resume command
is printed before waiting. The flow does not start probes, rotate credentials,
or restart applications. A deployment that stops being live cannot pass.

For scripts, use `bindings check APP --deployment ID` with explicit flags and
`--json`. Blocked checks return a nonzero exit status.

## Inspect Job task logs interactively

```sh
gregale jobs logs --interactive
# Bound the returned log payload:
gregale jobs logs --interactive --max-bytes 65536
```

Choose a Job by name, a run by status and creation time, and a task by index,
status, attempt number, and error. Each picker offers more pages and cancellation.
The CLI reads the selected task's log snapshot and prints the equivalent command,
including the active profile and log byte limit. Empty output and truncation are
reported using the same renderer as explicit log inspection.

This flow does not retry tasks or start new runs. Task logs can change if the
selected task is running or retried while you browse; the picker shows the
attempt observed during selection. For scripts, use
`jobs logs NAME RUN_ID TASK_INDEX --max-bytes N --json`.

## Retry a Job task interactively

```sh
gregale jobs retry --interactive
```

Choose a Job, run, and task through paginated pickers. Tasks are filtered to
failed, timeout, out-of-memory, and cancelled states. Review the current error,
attempt count, retry maximum, and eligibility before confirming one retry.
The CLI rereads the task and retry policy before submission and stops if they
changed. This client-side recheck is not an atomic lock; the server checks
status, remaining budget, image readiness, and flexible start-window eligibility.

A retry executes the task again and can repeat its effects. Cancellation before
confirmation submits nothing. After acceptance, the CLI prints commands to
inspect tasks and logs. For scripts, use `jobs retry NAME RUN_ID TASK_INDEX`.

## Follow a Job run

```sh
gregale jobs wait --interactive
gregale jobs wait my-job RUN_ID --timeout 10m
```

Choose a Job and run through paginated pickers, or provide the name and UUID.
The CLI polls that exact run and prints changed status and task counts. Waiting
only reads status; interrupting or timing out does not cancel the run.
`--poll-interval` defaults to 2 seconds and `--timeout` to 10 minutes; both
must be positive. A command to resume waiting is printed before observation.

Exit codes are 0 for succeeded, 1 for failed/cancelled/dead-letter or read errors,
124 for timeout, and 130 for interruption. With `--json`, an explicit target
returns one final receipt containing the last observed run (when available),
exit code, resume command, and any read/wait error. Retried runs can reopen;
waiting stops at the first terminal status it observes.

## Start a Job run interactively

```sh
gregale jobs run --interactive
```

Choose a Job through a paginated picker. The Job must be active with a ready
image. Choose a task count (default 1) and parallelism (defaults to the Job's
setting), then review its image, command, RAM per task, timeout, and retry policy.
The flow inherits the Job environment and failure rules; environment values are
not printed. The server enforces account plan limits.

Confirm before starting one run. The CLI rereads the Job configuration before
submission and stops if it changed; this client-side check is not an atomic lock.
After acceptance, a command to follow the returned run is printed. Optionally
follow it for up to 10 minutes using the same read-only poller as `jobs wait`.
Declining to follow leaves the accepted run running. Timeout and interruption
only stop observation.

For input manifests, environment overrides, flexible execution, or scripts,
use `jobs run NAME` with explicit flags.

## Cancel a Job run interactively

```sh
gregale jobs cancel --interactive
```

Choose a Job and an unfinished run through paginated pickers. Only queued and
running runs are shown. Review fresh task counts and confirm cancellation.
The CLI rereads progress after confirmation; if the run finished, it submits
nothing, and if status or counts changed, it asks you to rerun the command to
review current progress. This client-side check is not an atomic server lock.

Cancellation stops pending work and requests termination of running tasks; it
cannot undo effects already produced. An inspection command is printed before
submission, including the active profile, so it is available if the request
fails or times out. The response confirms recorded cancellation, not that every
running task has finished terminating. Use `jobs tasks NAME RUN_ID` to inspect.
Explicit `jobs cancel NAME RUN_ID` remains available for scripts.

## Edit Job settings interactively

```sh
gregale jobs update --interactive
```

Choose a Job and edit prefilled RAM, per-task timeout, maximum parallelism,
retry maximum, and active/paused state. Review changed values and confirm before
saving. Unchanged fields are omitted; keeping all values unchanged makes no
update request. Account plan limits are enforced by the server.

The CLI rereads configuration before saving and stops if it changed. Runtime
schedule timestamps are ignored by this client-side check, which is not an
atomic server lock. Pausing prevents future dispatches without canceling running
tasks. For images, commands, environment variables, schedules, or scripts,
use `jobs update NAME` with explicit flags.

## Create a Job interactively

```sh
gregale jobs add --interactive
```

Enter a Job name and container image. Keep the image entrypoint, or enter an
executable and additional arguments individually (up to 64 command entries).
Arguments are literal values, without shell splitting or comma parsing; the
review shows the argument array. This flow accepts nonempty arguments.

Keep server resource defaults, or choose RAM, timeout, parallelism, and retries.
Choose batch or recurring execution. For a recurring Job, choose an explicit
IANA timezone and a schedule preset (every 5 minutes, hourly, daily at 09:00,
or weekdays at 09:00), or enter a custom five-field cron expression. The shared
Gregale cron grammar validates the expression and previews three nominal times
with timezone offsets before confirmation. Actual runs may be delayed or skipped
under the default scheduling policy.

Review the settings and confirm before creating one Job. The server validates
the image and account plan limits. Batch creation prepares the image without
starting a run. Recurring creation enables scheduling, so runs can start
automatically once the image is ready. The result shows effective resources, image preparation status,
and a profile-aware inspection command. Once ready, use `jobs run --interactive`.

For environment variables, empty command arguments,
custom policies, or scripts, use the explicit CLI or API as appropriate.

## Wait for a Job image to become ready

```sh
gregale jobs info my-job --wait-ready --timeout 5m
```

Follow image preparation without starting a run. The wait shows status changes,
returns when ready, and reports image preparation failures. Guided creation
also offers this wait for a pending image. The wait pins Job identity and image
reference, then pins the resolved digest once available; a changed target stops
observation. A printed resume command reads current configuration on a new wait.

`--timeout` defaults to 5 minutes and `--poll-interval` to 2 seconds; both must
be positive. Exit codes are 0 for ready, 1 for failure/read errors/changed target,
124 for timeout, and 130 for interruption. With `--json`, one final receipt
contains the last observed Job, exit code, resume command, and any error.
Interrupting or timing out leaves image preparation running independently.

## Inspect retained Job attempts interactively

```sh
gregale jobs attempts --interactive
```

Choose a Job, run, task, and retained attempt through paginated pickers.
The detail view shows status, exit code, start/finish times, outcome code,
retry decision, and error information. Optionally display the output retained
for that attempt, with empty-output and truncation indicators. The view reads
terminal attempt records and never retries a task or starts a run.

A profile-aware command is printed to list the selected task's history again.
For scripts, use `jobs attempts NAME RUN_ID TASK_INDEX --json`.
Retained history and output depend on server retention; ongoing attempts may
not yet have a terminal record.

## Choose a Job artifact interactively

```sh
gregale jobs artifact-url --interactive
```

Choose a Job, run, and task through paginated pickers, then select a managed
artifact from its validated output manifest. The picker shows artifact names,
byte counts, and SHA-256 digests. External-storage artifacts are counted but
require access through their storage provider.

The CLI rereads the selected task and stops if its attempt or selected artifact
changed. It requests the signed link using the existing server verification
endpoint and checks returned name, size, and digest against the selection.
The printed command requests a fresh link later using the active profile.
This flow does not download bytes locally. For scripts, use
`jobs artifact-url NAME RUN_ID TASK_INDEX ARTIFACT_NAME --json`.

## Replay unsuccessful Job inputs interactively

```sh
gregale jobs replay-failed --interactive
```

Choose a Job and source run, then review its unsuccessful tasks, input IDs,
errors, and execution policy. The source must be terminal with a complete task
inventory, and the current Job image must be ready with the same resolved digest
as the source snapshot. Failed, timeout, OOM, and cancelled tasks are reviewed.

Confirm creation of one linked recovery run. The CLI rereads source tasks and
configuration before submission; changes stop the flow. This client-side check
is not an atomic lock. The server checks plan limits and selects replay tasks
atomically when creating the run. Inputs execute again and can repeat effects.

After acceptance, a profile-aware follow command is printed. Optionally follow
the returned recovery run for up to 10 minutes. Stopping observation does not
cancel it. For scripts, use `jobs replay-failed NAME RUN_ID` with explicit IDs.

## Quick reference

| Shell | Command | Install path (user) | Install path (system) |
|---|---|---|---|
| bash | `gregale completion bash` | `~/.local/share/bash-completion/completions/gregale` | `/usr/share/bash-completion/completions/gregale` |
| zsh | `gregale completion zsh` | `${fpath[1]}/_gregale` (usually `~/.zsh/completions/_gregale`) | `/usr/share/zsh/site-functions/_gregale` |
| fish | `gregale completion fish` | `~/.config/fish/completions/gregale.fish` | `/usr/share/fish/vendor_completions.d/gregale.fish` |
| powershell | `gregale completion powershell` | profile-script snippet (see below) | n/a |

## bash

```bash
# System-wide (requires sudo, persists for all users):
sudo gregale completion bash > /usr/share/bash-completion/completions/gregale

# User-local (no sudo, survives OS upgrades):
gregale completion bash > ~/.local/share/bash-completion/completions/gregale

# One-shot, current shell only (great for testing):
source <(gregale completion bash)
```

macOS users: bash 3.2 ships as `/bin/bash`. The completion script is
compatible — no `mapfile`, no associative arrays. For bash 4+ (via
Homebrew `bash`), the script still works; bash 5+ even offers the
slightly faster `compopt -o nospace` we could turn on in a future
PR.

## zsh

```bash
# System-wide (persists for all users):
sudo gregale completion zsh > /usr/share/zsh/site-functions/_gregale

# User-local:
gregale completion zsh > ~/.zsh/completions/_gregale
# then add this to ~/.zshrc (only if not already on fpath):
#   fpath=(~/.zsh/completions $fpath)
#   autoload -U compinit && compinit
```

The script emits `#compdef gregale` as the first line; zsh's
autoload machinery picks it up automatically when the file lands on
$fpath. After install, restart the shell or run `rehash` +
`autoload -U compinit && compinit`.

## fish

```bash
# User-local (no sudo):
gregale completion fish > ~/.config/fish/completions/gregale.fish

# System-wide:
sudo gregale completion fish > /usr/share/fish/vendor_completions.d/gregale.fish
```

Fish picks up completion scripts automatically; no `rehash` needed.
Reload the current shell with `exec fish` to see new completions
immediately.

## powershell

Add this snippet to your PowerShell profile (`$PROFILE`):

```powershell
gregale completion powershell | Out-String | Invoke-Expression
```

The snippet registers an argument completer via
`Register-ArgumentCompleter -Native -CommandName 'gregale'`. Re-run
the snippet after upgrading gregale if completion starts misbehaving.

## man pages

```bash
# Render the top-level gregale(1):
gregale man | man -l -

# Render a per-command page (e.g. gregale-alerts(1)):
gregale man alerts | man -l -

# Install permanently (system-wide):
sudo install -m 0644 <(gregale man) /usr/local/share/man/man1/gregale.1
sudo install -m 0644 <(gregale man alerts) /usr/local/share/man/man1/gregale-alerts.1
sudo mandb   # refresh the man-db index
man gregale-alerts
```

The per-command man page slug is `gregale-<command>` (e.g.
`gregale-alerts`, `gregale-delayed-task`). When `gregale man
<command>` is run with a non-existent command, exit 1 surfaces the
unknown-command error path explicitly.

## Slug cache (how completion knows your app names)

The per-account positional completion paths (e.g. `<slug>` in
`gregale app <slug> ...`) read from a JSON file at
`~/.config/gregale/completion-cache.json`. The file is rewritten
on every successful `gregale apps` / `gregale orgs` call (the
`pkg/api/client.go::doReq` middleware does the rewrite; no user
action needed).

The file is keyed by:

- `apps`: list of `{id, slug, name}` records from `GET /v1/apps`.
- `orgs`: list of `{id, slug, name}` records from `GET /v1/orgs`.
- `saved_at`: RFC3339 timestamp.

To force a refresh, `rm` the file:

```bash
rm ~/.config/gregale/completion-cache.json
gregale apps      # repopulates the cache
```

The cache file is mode 0600 and the dir is mode 0700 — equivalent
visibility to a token-respecting `gregale apps` call. There's no
need to seal it like env secrets; the contents are public to the
account owner.

## Why no checked-in `contrib/completion/`?

The binary is the source of truth. Every `cliCommand{}` entry in
`cli_meta.go` shows up in all four shell backends at compile time,
and the manifest-drift test fires when a new command ships
without a matching entry. A checked-in copy would drift the
moment the manifest changes — the operator would either re-run
the install script (defeating the point of checking it in) or
ship a stale completion script.

## Why no checked-in `docs/man/`?

Same reasoning: the man pages are rendered from the manifest at
process boot, so they always reflect the current binary. A checked-
in copy would drift the same way.

## See also

- ADR-083 (this design decision)
- `docs/faas_ux_spec.md` §3.2 (`--help is a real doc`)
- `docs/source-ref.md` — headless `gregale deploy --repo --ref` for CI runners
