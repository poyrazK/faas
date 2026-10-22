# gregale CLI — shell completion + man pages setup

The gregale CLI emits shell completion scripts and man pages from the
binary itself. No checked-in copies in `contrib/completion/`; the
binary is the source of truth. This doc covers the install path for
each shell, plus the man-page install for offline / CI use.

## Choosing a task

Use `logs views delete NAME --dry-run` to check preferred-view impact before
deleting. A preferred name gets a warning and an after-deletion repair command;
an unreadable preference gets inspection guidance. Neither command is executed
by the preview, and local files remain unchanged.

After renaming or deleting the preferred log view, the operation receipt shows
how to repair the preference: select the renamed view with `logs defaults set
NEW`, or clear a deleted preference with `logs defaults clear`. You can also
select another saved view. Commands pin the current profile and are not run
automatically. Unreadable preferences produce inspection guidance while the
view operation retains its normal outcome.

Preview duplicating or renaming reusable log filters with
`logs views copy errors checkout-errors --dry-run` or
`logs views rename errors production-errors --dry-run`. The preview shows the
destination, preserved filters, and whether the source remains. Renaming a
preferred view leaves its preference pointing at the old name; the preview
provides a command to update that preference after the rename. An unreadable
preference is reported as unknown. Previews do not write files or query logs.

Preview creating a saved log view with
`gregale logs views save errors --level error --since 1h --dry-run`.
The preview shows proposed filters, the storage path, and copyable commands
without saving or querying logs. Add `--replace` when previewing replacement of
an existing name; before/after filters show what would change. Latest HTTP error
views support this preview too. Remove `--dry-run` to save the reviewed filters.

Run `gregale context presets` to list saved project, app, and environment
contexts using the existing `context presets list` behavior. It shows their
relationship to the current context without applying a preset or making API
requests or writes. Use `context presets --help` for help.

Run `gregale profile` to list configured connections and see which is selected.
It uses the existing `profile list` output without changing the active profile,
reading credentials, or making network requests. Use `profile --help` for help.

Run `gregale logs views` to list reusable saved log filters without specifying
`list`. It retains the existing local list output and JSON format, makes no
writes or API requests, and uses `--help` for command help.

Add list options directly to this shortcut: `logs views --source http` or
`logs views --name errors` filters the listing; `gregale --json logs views
--details` includes filters and copyable commands. `--app APP_SLUG` targets those
commands at an explicit, unverified app without checking remote access.

The human list marks the preferred view for `logs --saved-view`; detailed JSON
adds a `preferred` boolean to each entry. Unreadable local preferences produce
null markers and an unknown-preference message in human output, while valid
views remain listable. With no preference saved, all markers are false. Plain
JSON listings continue to return only names and do not read preferences.

Run `gregale config` to inspect effective CLI settings and where they came from.
It uses the same local, read-only behavior as `gregale config list`, including
JSON output and configuration validation. Use `gregale config --help` for help.

Run `gregale menu` in a terminal to choose guided app setup, app inspection,
logs, secret changes, issue inspection, a Job run, or workflow inspection.
The menu prints the command it opens and reuses that command's app pickers and confirmations.
It preserves the selected connection profile and saved project context.

Choose Exit (the default) to leave without opening a command. EOF closes the
menu; Ctrl-C cancels it. The menu requires terminal input and output and
rejects JSON output and `--non-interactive`. Use `gregale help` and explicit
commands for scripts. Running bare `gregale` continues to show help.

## Guided workflow inspection and recovery

Run `gregale workflows status --interactive` to choose an app and workflow run,
then read its status, deployment, step states, attempt counts, and errors. The app
comes from `--app`, a saved link, or the existing app picker. Run selection is
paged and scoped to that app. `gregale menu` also opens this inspection flow.

Use `gregale workflows retry --interactive` to choose a failed or dead run
and a step that passes the server's manual HTTP retry preview. Review its
attempt and the skipped descendants that would reopen, then confirm (default:
no). The preview uses the captured workflow graph and current quota; it does
not reserve capacity. Retry can execute HTTP effects again. Older servers
without the retry preview offer no steps; explicit retry commands remain
available.

Use `gregale workflows cancel --interactive` to choose a pending, running,
or waiting run. Review its current steps and confirm (default: no).
Cancellation stops further progress and does not undo completed external effects.

Both changes re-read the run and steps after confirmation and stop if the
reviewed state changed. The server remains authoritative when accepting the
request. Responses must match the selected app and run. The printed inspection
and action commands preserve the connection profile and quote arguments for
POSIX shells.

Interactive workflow commands require terminal input and output and reject
JSON output, `--non-interactive`, and mixed explicit run/step arguments.
For scripts, keep using `workflows status RUN_ID`,
`workflows retry RUN_ID STEP_NAME`, and `workflows cancel RUN_ID`.

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
HTTP views support status, route, and page size. Supply an app target and
compatible one-off overrides alongside `--view`; add `--dry-run` to preview
them without changing the saved view. Update the view to persist filter changes.

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
retry maximum, and active/paused state. Keep, set/change, or remove the recurring
schedule. Schedule editing prefills the expression and timezone, offers presets,
and previews three nominal times before saving. Removing the schedule converts
the Job to batch without canceling existing runs. A batch Job can become recurring
by setting a schedule. Review changed values and confirm before
saving. Unchanged fields are omitted; keeping all values unchanged makes no
update request. Account plan limits are enforced by the server.

The CLI rereads configuration before saving and stops if it changed. Runtime
schedule timestamps are ignored by this client-side check, which is not an
atomic server lock. Pausing prevents future dispatches without canceling running
tasks. Existing custom scheduling policy is retained; nominal times do not
guarantee execution, which can be delayed or skipped. For images, commands,
environment variables, custom scheduling policy, or scripts,
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

## Browse recurring Job decisions interactively

```sh
gregale jobs occurrences --interactive
```

Choose a Job, then browse occurrence history with older-page navigation.
Inspect a scheduled time, status, reason, outcome/retry decision, scheduling
policy, start deadline, execution timestamps, and blocking occurrence when
present. These are recorded decisions, which can evolve while you browse.
Choosing another occurrence rereads the page.

A command to read the current history page is printed with the active profile.
When an occurrence has a run, the CLI verifies its Job and occurrence identity
and prints a `jobs wait` command. The flow only reads history; it never starts
or retries a run. For scripts, use `jobs occurrences NAME --json` with explicit
pagination flags.

## Set Job registry credentials interactively

```sh
gregale jobs registry set --interactive
```

Pick a Job, enter a registry host and username, then enter a hidden password or
token. The review identifies whether the selected host's credential will be
added or replaced, showing the existing username for replacement. New entries
are checked against the returned quota before token entry.

Confirm before saving. The CLI rereads Job identity and selected credential
metadata and stops if either changed; last-use timestamps do not block saving.
This client-side check is not an atomic server lock. All prompts share one raw
terminal reader, and terminal echo is restored before the write request.
Token contents are not printed. For scripts, use the existing explicit command
with `--password-stdin`.

## Remove a Job registry credential interactively

```sh
gregale jobs registry rm --interactive
```

Pick a Job and credential, then review the host, username, creation/update times,
and last-used time. Confirm removal after reviewing the effect on future private
image pulls. No password or token is retrieved or shown.

The CLI rereads Job identity and selected credential metadata before deletion.
Changed credentials stop the flow, while an already removed credential skips
the deletion. Last-use timestamp updates alone do not block removal. This is a
client-side check, not an atomic server lock. Explicit
`jobs registry rm JOB --registry HOST` remains available for scripts.

## See upcoming Job schedules

```sh
gregale jobs next
gregale jobs next --json
```

Read all Job pages and sort active recurring Jobs by their next nominal cron
time, displayed in each Job's explicit timezone. The overview includes Job state,
kind, timezone, and image preparation state. Paused and batch Jobs remain visible
without a next dispatch time. A pending image still has a nominal expression
time, which does not imply that execution will start.

The JSON receipt contains one shared `as_of` timestamp and the sorted items,
including scheduling policies and execution notes. Invalid recurring schedules
or timezones are reported per Job and return a nonzero exit status.
Expression times are candidates: image readiness, overlap, start deadlines,
missed-run policy, and delays affect actual execution. This command only reads
Job metadata; use `jobs occurrences` to inspect recorded schedule decisions.

## Browse Job tasks interactively

```sh
gregale jobs tasks --interactive
```

Choose a Job and run, then browse all tasks or only unsuccessful tasks
(failed, timeout, OOM, cancelled). Navigate forward through task pages,
refresh the current page, or switch filters (which restarts at the first page).
Each page shows fresh run counts; task labels include status, attempt, and error.
Filtering applies to each server page, so a page can contain no matching tasks
while later pages do.

Select a task to inspect outcome, retry decision, error, and execution timestamps.
Exit code is shown only for a task with a recorded finish time. Commands for
that task's logs and retained attempt history are printed with the active profile.
Continuing rereads the page. The flow only observes work; it never retries or
cancels tasks. Explicit `jobs tasks NAME RUN_ID` remains available for scripts.

## Remove a Job interactively

```sh
gregale jobs rm --interactive
```

Choose a Job, then review its image, command arguments, resources, state,
and recurring schedule. Confirm before removal. The CLI rereads identity and
configuration and stops if they changed; schedule activity timestamps alone
are ignored. This client-side check is not an atomic lock.

Removal soft-deletes the Job and stops future dispatches. The server blocks
removal while queued or running tasks remain and returns its explanation;
use `jobs cancel` or `jobs wait` to address that work first. The guided flow
does not cancel tasks automatically. Explicit `jobs rm NAME` remains available
for scripts.

## Inspect issues interactively

```sh
gregale issues get --app my-api --interactive
# Use the linked app, or choose one:
gregale issues get --interactive
```

Choose an issue state (open, resolved, ignored, or all) and ordering (recent or
verified customer impact over 24 hours). Browse cursor pages labeled with issue
titles, state, last-seen time, and verified customer count; unavailable impact
is shown as unknown. Selecting an issue reads fresh evidence, release history,
activity, and impact using the existing JSON detail view.

A profile-aware command is printed for the selected issue, plus commands for
additional event, release, or activity pages when available. This flow only
reads issues and never changes ownership or resolution state. For scripts,
use `issues get ISSUE_ID --app APP` with explicit history cursors.

## Resolve an issue interactively

```sh
gregale issues resolve --app my-api --interactive
# Use the linked app, or choose one:
gregale issues resolve --interactive
```

Choose an open issue and a fixing deployment from paginated release history.
Review the fresh issue title/environment and selected release status/scope,
then confirm resolution. This records your assertion that the selected release
fixed the issue; it does not deploy code or verify the fix.

The CLI rereads both targets before saving and stops if either changed. This
client-side check is not an atomic server lock. The server enforces resolution
rules and the returned receipt is checked against the selected app, issue,
and fixing deployment. For scripts, use
`issues resolve ISSUE_ID --app APP --deployment DEPLOYMENT_ID`.

## Temporarily ignore an issue interactively

```sh
gregale issues ignore --app my-api --interactive
# Use the linked app, or choose one:
gregale issues ignore --interactive
```

Choose an open issue, then select 1 hour, 24 hours, 7 days, or a custom Go-style
duration (for example `48h`, from 1 second through 90 days). Review the exact
expiry in UTC and confirm before saving. The expiry is fixed at review time,
so time spent reviewing reduces the remaining ignore period.

The CLI rereads the issue after confirmation and stops if it changed or if the
expiry already passed. This client-side check is not an atomic server lock.
The returned ignored state and expiry are checked against the requested values.
A profile-aware inspection command is printed after saving. For scripts, use
`issues ignore ISSUE_ID --app APP --until RFC3339`.

## Reopen an issue interactively

```sh
gregale issues reopen --app my-api --interactive
# Use the linked app, or choose one:
gregale issues reopen --interactive
```

Choose resolved or ignored issues, then pick an issue by title. Review its
current state, fixing release and resolution time, or suppression expiry in UTC.
Confirm to return it to open and clear the current resolution and suppression;
historical activity remains available.

The CLI rereads the issue after confirmation and stops if it changed. This
client-side check is not an atomic server lock. The returned issue identity,
open state, and cleared resolution and suppression are checked before reporting
success. A profile-aware inspection command is printed afterward. For scripts,
use `issues reopen ISSUE_ID --app APP`.

## Assign an issue interactively

```sh
gregale issues assign --app my-api --interactive
# Use the linked app, or choose one:
gregale issues assign --interactive
```

Choose an open issue by title, then choose **Assign to me** or **Unassign**.
Assign to me reads the authenticated account identity. Review the current owner
and proposed owner before confirming. If ownership already matches, the command
exits without a write.

The CLI rereads the issue after confirmation and stops if it changed. This
client-side check is not an atomic server lock. The server enforces assignment
permissions. The returned issue identity, state, and owner are checked before
reporting success, followed by a profile-aware inspection command. For scripts
or assignment to another account, use
`issues assign ISSUE_ID --app APP --assignee ACCOUNT_ID`; omit `--assignee` to
unassign.

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

## Following submitted work

A successful workflow or Job submission confirms acceptance, not completion.
Human-readable run receipts show the returned status and copyable commands
that retain the selected profile. Workflow receipts point to `workflows status`
(and `workflows diagnose` for failures). Job run and replay receipts point to
`jobs wait` for completion and `jobs tasks` for individual results. Workflow
cancellation reports when an already-finished run was left unchanged.
Cron creation distinguishes a saved schedule from a completed execution and
preserves the selected profile in its trigger command. These hints do not
change the JSON response format.

## Recovering an uncertain run submission

When a Job run, failed-input replay, or workflow start cannot be confirmed,
human-readable errors include a profile-preserving command to inspect recent
runs before resubmitting. A missing run in the list does not prove rejection:
the original request may still be in progress. For workflow starts submitted
with `--idempotency-key`, retry with the original key and unchanged input and
target, subject to server replay retention limits. Explicit client rejections
retain their existing remediation. JSON error envelopes and exit codes are
unchanged.

## Reading the inspection overview

`gregale inspect APP` starts with a short human-readable overview, the main
known reason, and one copyable next command that retains the selected profile.
Known errors take priority, followed by blocked recovery, processing operations,
unavailable information, and warnings. The detailed observations and all
recommendations remain below. “No urgent issues reported” means this snapshot
has no error or warning recommendations; it is not a claim that every route or
workload is healthy. JSON snapshots retain their existing schema and contents.
The same overview appears in human-readable watch snapshots.

## Checking secret reference configuration

`gregale secrets refs list --app APP --environment ENV` now checks whether each
configured source secret is present in that same environment. It reads names
and metadata only. Missing sources include a command using `--from-stdin` and
an explicit scope; supply an existing private file named `secrets.env` containing
`KEY=VALUE` lines, rather than typing values into shell commands. This check
covers configured environment references, not undeclared application requirements
or deployment manifest references. Presence does not prove application delivery.
If source metadata cannot be read, availability is unknown and the command fails.
JSON reference listings retain the original response and do not make this extra
metadata request.

Human-readable secret update and removal receipts now show the app, scope,
scoped delivery inspection command, and a command to follow a requested restart
or request a fresh boot. Running instances retain their boot environment unless
reload support is configured; review delivery and application acknowledgements.
Reference edits apply on future cold wakes. A fresh restart affects the app's
active workloads, so verify the serving deployment scope first. Changing a
secret value does not change the deployed code or its secret allowlist.

## Understanding a deployment failure from inspect

The human-readable `gregale inspect APP` overview includes the selected failed
deployment's persisted cause and recovery instruction when available. It reuses
the existing deployment history response without another request. Each field is
limited to 400 characters; an exact deployment command provides the full details.
A newer failed zero-traffic candidate is labeled separately from the release
still serving traffic. No explanation is inferred when persisted text is absent.
Log excerpts stay in the detailed failure view. JSON summaries retain their
existing fields and schema. Human-readable watch output refreshes the overview
when the persisted explanation changes.

## Reading deployment readiness

Human-readable `deploy --dry-run`, `--diff`, and `--server-diff` previews print a
short readiness overview on stderr before the stable diff on stdout. It shows
the app, connection profile, default target scope, inferred runtime/start command
when available, and the first blocking finding or warning. Named environment
previews remain unsupported; the overview does not imply that another environment
was checked. `--lenient` still changes only the exit-code gate. No findings does
not guarantee a successful build or runtime.

Doctor's human-readable report now leads with its first error (or first warning
when no errors exist), existing recovery instruction, and source location before
the full check list. Existing strictness, checks, and JSON formats are unchanged.

## Going from a failure to evidence

Inspection overviews with persisted deployment failures and `inspect --errors`
include a log command pinned to the failed deployment, including a failed
candidate rather than the serving release. `jobs tasks JOB RUN_ID` prints scoped
log commands for up to three failed/dead-letter tasks, sorted by task index;
other failed task indexes remain in the table. A failed `jobs wait` receipt points
to that task view. Workflow status failures link to diagnostics and the current
step's attempts, or to the step list when no current step is available. These
commands retain the selected profile. Logs may expire; retained Job attempt
outcomes remain a separate evidence source. JSON output is unchanged.

## Checking when saved configuration takes effect

Human-readable `env push` receipts now show the app and scope, a scoped secret
delivery command (env push stores values as sealed secrets), and a command to
follow the requested fresh restart or request one after reviewing the serving
scope. Without a restart, running instances retain their environment until a
cold wake. A saved value does not by itself confirm application acknowledgement.

App-setting and scaling receipts list the saved field names without their values,
identify the app and environment, and include a command to reread that same
configuration. Default app updates also point to current operations through
`inspect`. Routing, scheduling, and boot settings have different application
paths; the update response confirms saved intent, not runtime convergence. Named
environment updates do not confirm a release there. These receipts do not
request extra mutations or change the JSON response format.

## Understanding empty lists

Empty app, deployment, Job, and workflow-run lists now identify the selected
connection profile and offer a copyable next command. Workflow lists echo their
status, name, time, and offset filters. Empty deployment cursor pages are distinct
from an empty deployment history; their next command returns to the newest page.
Job lists distinguish the first page from an offset page. Empty run lists do not
prove that an uncertain submission was rejected. Existing JSON envelopes and
NDJSON output remain unchanged. Completed non-follow runtime log queries with
no returned lines show the app, deployment and filters, with an inspection
command. Filters and retention can limit results; active follow streams and
JSON log output keep their existing behavior.

## Reviewing recent observed changes

Human-readable `inspect` summaries show up to three newest observations from the
already-loaded deployment submissions and pending/failed recovery operations.
Each row identifies its reported status, scope when available, and whether its
UTC timestamp is submission, request, or update time. Missing timestamps are
labeled unavailable. Each row includes a profile-preserving command for that
exact deployment, rollback, or restart. Deployment history remains one command
away. This is partial history, not an audit of all configuration changes, and
nearby timestamps do not establish causation. JSON summaries and JSON watch
contents retain their existing schema; human watch output includes these rows.

## Distinguishing processing from health verification

Human-readable `inspect` output separates deployment-time verification receipts
from production route evidence, including the available evaluation time and
coverage, and provides a profile-preserving default-scope `app APP health`
command. This adds no API request to inspection. Health assessment output names
its serving deployments and request-evidence timestamp when supplied. A reported
healthy assessment is bounded by its scope, timestamp, and validity; this read
uses recorded evidence and does not wake the app or probe every route.

Restart receipts include request/completion timestamps when available and a
copyable next command. Pending operations link to restart status, failed
processing links to inspection, and completed processing links to HTTP health.
Completing a restart does not establish application health. JSON responses and
existing exit codes are unchanged.

## Scanning the consolidated inspection output

The human-readable overview has one primary next command. Persisted failure
recovery and deployment log commands appear below it in a dedicated detail
section. Deployment-check timestamps remain beside runtime evidence; current
route-health timestamps and coverage stay in Current operations, replacing the
separate duplicate Verification section. A primary recommendation's explanation
is not repeated at the end, but its recovery advice remains available. Identical
additional recommendations are deduplicated in human output only. A snapshot
with no urgent recommendations points to the existing HTTP health assessment.
JSON snapshots, observations, and recommendations remain unchanged.

## Recovering partial configuration operations

Human-readable errors for partially completed env pushes, multi-secret updates,
secret removals, and rotations identify confirmed writes separately from an
unconfirmed restart or incomplete acknowledgement. They show confirmed key names
without values and include a scoped metadata command. When a restart identifier
is known, the recovery command targets that exact operation; otherwise it points
to current operations before another restart is requested. Failed writes may have
been accepted despite a transport error, so the guidance does not recommend
repeating the whole operation. Existing JSON receipts and error exit codes remain
unchanged. Deployment submissions retain their existing recovery receipts.

## Recognizing the write target

Before non-interactive `secrets set`, `secrets unset`, `secrets rotate`, and
`env push` write values, human output shows the connection profile, app, and
resolved scope on stderr. App and scope labels distinguish explicit flags,
linked project context, and the default scope. An explicit `--profile` is also
identified; otherwise the selected connection profile is shown. Profile names
identify local connections, not verified account identities. The preview adds
no confirmation prompt, prints no secret values, and leaves JSON output unchanged.

## Previewing secret changes

Use `--dry-run` with `secrets set`, `secrets unset`, or `secrets rotate` to
validate local input and review the target, key names, retention class when
specified, and requested restart or acknowledgement wait. Stdin input and
`--json` are supported. Values are never included in the preview. No API
requests, writes, restarts, or waits occur; app existence, permissions, quotas,
and runtime delivery remain unchecked. Interactive flows do not accept dry-run.

```sh
gregale secrets unset --app my-api OLD_API_KEY --scope staging --dry-run
```

## Linked apps for secret commands

After `gregale link <project-slug> --app <slug>`, `secrets list`, `set`,
`unset`, and `rotate` use that app when `--app` is omitted. Explicit `--app`
overrides the link. Missing, invalid, or ambiguous context fails with guidance
to supply an app or link again. Human write previews and JSON dry-run receipts
identify whether the app came from a flag or the linked project.

```sh
gregale secrets list
gregale secrets set --from-stdin --dry-run < secrets.env
gregale secrets unset OLD_API_KEY --dry-run
```

## Understanding empty secret lists

Empty human `secrets list` results show the app, connection profile, selected
scope, and active class or age filters. Filtered results suggest removing those
filters in the same scope; age-filter guidance explains that unknown update
timestamps are excluded. Empty scoped results suggest checking `__all__` and
show a stdin-based dry-run command for adding values. An all-scopes result uses
an explicitly labelled default-scope example for writes. Empty detection uses
returned rows rather than the cross-scope quota count. JSON output is unchanged.

## Reading secret delivery at a glance

Nonempty human `secrets list` output begins with delivery counts for the returned
rows after filters: delivered at the current version, pending, failed, and unknown
or unconfirmed. A delivered record requires matching nonzero delivery versions.
The overview keeps runtime-start delivery distinct from runtime reload and
application acknowledgement, whose per-row details remain available. Failed,
pending, or unknown delivery suggests inspecting current operations before a
recovery action; pending alone is not labelled a failure. The overview uses the
existing response, adds no API calls, and leaves JSON unchanged.

## Filtering secret delivery

`secrets list --delivery-status delivered|pending|failed|unknown` filters the
returned metadata locally using the same version checks as the delivery overview.
`unknown` includes missing or unexpected statuses and delivered records whose
versions do not confirm current delivery. This checks runtime-start delivery,
not runtime reload or application acknowledgement. The filter combines with
`--class`, `--older-than`, and `--scope __all__`, works with JSON, and adds no API
calls. With a delivery filter, `count` reports matching rows while `quota_max`
remains unchanged. Empty-result guidance provides an unfiltered command.

```sh
gregale secrets list --delivery-status failed
gregale secrets list --scope __all__ --delivery-status unknown
```

## Watching secret delivery

`gregale secrets list --watch --interval 5s --timeout 2m` takes an initial
metadata snapshot, then prints updates when the returned metadata changes.
The app and scope are resolved once. Class, age, and delivery filters apply to
every read; leaving a filtered result is a change too. Row ordering and
observation timestamps alone do not trigger updates. Runtime reload and
application acknowledgements remain distinct from delivery.

With `--json`, each output line is a versioned `snapshot`, `change`, or
`unavailable` event; snapshots and changes contain the usual list response in
`summary`. Temporary read failures emit one unavailable event, suppress stale
results, and retry; recovery emits a new snapshot. Permanent access/not-found
errors stop the watch. Each poll has a 30-second read limit. Intervals must be
between 1s and 1h; a zero timeout watches until interrupted. Timeout exits 124;
interruption exits 130. `--interval` and `--timeout` require `--watch`. Watching
never writes values or requests restarts. Ordinary list JSON remains unchanged.

## Recognizing app settings targets

Before direct `app <slug>` setting updates or `app <slug> scale` mutations,
human stderr shows the selected connection profile, app, target environment,
and sorted setting names without values. A named environment is labelled as
coming from `--environment` or a saved scale plan; omitting it targets app-level
settings, rather than inheriting the linked project's environment. Applying a
saved scale plan also shows the connection target alongside its existing plan
preview. Read-only app output and `scale --plan` retain their existing behavior.
No confirmation prompts or API calls are added, and JSON output is unchanged.

## Plain-language settings receipts

Human app-setting target previews and saved receipts use readable labels such as
“Idle timeout (seconds)” and “Minimum warm instances,” reusing scale-plan labels.
They show names without values and retain explicit zero, false, and reset fields.
An app-level update now says “App-level settings saved” rather than implying a
named `default` environment. Named-environment receipts retain their target.
Request payloads, JSON responses, and saved scale-plan formats are unchanged.

## Following selected secret keys

`secrets list --key DATABASE_URL --key API_TOKEN` selects exact key names;
repeat flags select their union, with duplicates ignored. Key selection combines
with scope, class, age, and delivery filters and works with `--watch` and JSON.
With `--scope __all__`, the same name can match in multiple scopes. Filtering is
local, adds no API calls, and makes `count` the number of matching rows while
preserving the quota. Empty output shows selected names and an unfiltered command;
it does not prove removal from running applications. Existing post-write metadata
commands now include the known keys for set, unset, rotation, and env push.

```sh
gregale secrets list --key DATABASE_URL --watch
```

## Waiting after setting secrets

`secrets set --wait-for-ack --timeout 2m` waits for application acknowledgements
for all updated keys, using one shared deadline after saving. Combine it with
`--restart` to request one fresh restart, follow its exact wake identifier until
running, and then check the authorized runtime roster, including the restarted
instance. `--timeout` requires `--wait-for-ack`; the wait is supported in dry-run
previews and excluded from interactive flows.

The PUT response does not include a version. The command records delivery
versions from metadata read immediately after saving and fails if those versions
change during the wait. Confirmation describes those observed versions, not
plaintext identity under concurrent writes. No active authorized runtimes produces
an explicit warning, not an application-confirmed result. A complete runtime
roster and application acknowledgements are required; delivery or signals alone
are insufficient. Unsupported reload or missing restart authorization fails with
recovery guidance. Waiting is read-only and never repeats writes or restarts.

On wait or restart failure, confirmed writes remain saved. JSON stdout retains a
receipt with keys, observed versions, acknowledgement progress, and any accepted
restart identifier, while stderr reports the error. Without `--wait-for-ack`,
existing receipts and behavior stay unchanged.

```sh
gregale secrets set --from-stdin --restart --wait-for-ack --timeout 2m < secrets.env
```

## Waiting after env push

`env push --wait-for-ack --timeout 2m` shares the acknowledgement engine used
by `secrets set`. It waits only for keys actually uploaded after secret scanning,
and combines with `--restart` to request one fresh restart and follow its wake.
A shared timeout starts after uploading and covers version reads, restart, and
acknowledgement polling. Without restart, confirmation requires application
acknowledgements from the active authorized runtimes; signals or delivery alone
are insufficient. No active targets produces an explicit warning. Version changes
or incomplete runtime rosters fail without repeating writes or restarts.

Existing env-push JSON receipts retain updated keys, count, apply mode, and any
accepted wake ID. Wait receipts also include observed versions, acknowledgement
status and progress, and a restarted instance ID when known. Errors preserve the
receipt on stdout and report the error on stderr. Dry-run previews show the wait
and timeout without uploading, restarting, or waiting. `--timeout` requires
`--wait-for-ack`; ordinary env push and preview JSON remain unchanged.

```sh
gregale env push -f .env --restart --wait-for-ack --timeout 2m
```

## Completing secret names

Regenerate or reinstall your shell completion script after upgrading to enable
secret-name suggestions for `secrets list --key` and the key positional in
`secrets unset` (including `rm`). Bash, zsh, fish, and PowerShell scripts use a
shared lookup that resolves explicit app/scope flags or linked project defaults.
List completion also supports `--key=PREFIX` and all-scopes metadata.
Suggestions include only valid existing key names, never values or hashes.

Names are cached privately for 30 seconds, separately by API connection, profile,
credential, app, and scope. Cache misses have an 800 ms read deadline. Failed or
unauthenticated lookups stay quiet and do not reuse expired names. Completion
never writes secrets or restarts an app. Names remain suggestions; commands
still validate their inputs and current server state when executed.

## Understanding unmatched secret keys

Human key-filtered lists explain selected names that do not appear, including
when other selected keys do match. A name present in the original fetched
metadata but excluded by class, age, or delivery filters gets a command to
review it without those filters. A missing name gets a suggestion only when a
single existing key is a clear close match; ties and short names receive no
guess. Suggestions stay within the metadata already returned for the selected
scope. They never change a mutation target or perform an extra API request.
Human watch updates include changes to this guidance; ordinary JSON and JSON
Lines event schemas remain unchanged. Missing metadata does not prove removal
from running applications.

## Reading secret watch changes

Human secret watching starts with the usual full snapshot. Later updates lead
with changed scoped key names and delivery/version transitions, application
acknowledgement counts, retention-class changes, or changed reload/roster evidence.
The current delivery overview remains visible without repeating every unchanged
row. A key newly appearing is described as entering the selected view; one leaving
is described as absent or filtered out, without asserting deletion. At most ten
changed keys are expanded per update, with a count and a current-list command for
more details. The command preserves the selected filters.

Unavailable reads reset the comparison, so recovery produces a fresh full
snapshot. Ordering and timestamp-only changes remain quiet. Selected-key guidance
and quota changes can still trigger updates. This uses existing poll responses
and leaves JSON Lines events and ordinary list output unchanged.

## Correcting mistyped flags

When an unknown flag has one clear close match among the current command's
supported flags, Gregale suggests the spelling, for example `--enviroment`
suggests `--environment`. Ambiguous matches and short names receive no guess.
The command still exits with an error; edit the flag and rerun it. Suggestions
appear in human output only; JSON errors retain their existing format and detail.

## Recognizing app read targets

Human `app <slug>` reads begin with the selected connection profile and an
app-level settings label. `--environment <slug>` instead identifies the explicit
named environment and explains that its configuration does not verify a deployment
or release there. URL and app status are labelled by that explanation as app
metadata rather than environment verification. The output also states that the
read does not deploy or probe health. Linked project environments are not silently
inferred for these app-setting reads. Existing details and read behavior remain
available, with no extra API calls and no changes to JSON output.

## See also

- ADR-083 (this design decision)
- `docs/faas_ux_spec.md` §3.2 (`--help is a real doc`)
- `docs/source-ref.md` — headless `gregale deploy --repo --ref` for CI runners

## Loading secrets from a file

Use a private file containing one `KEY=VALUE` pair per line. Blank lines and
comments beginning with `#` are ignored; values may contain `=`. This uses the
same plain pair format as `secrets set --from-stdin`, not shell evaluation.

```sh
gregale secrets set --file secrets.env --dry-run
gregale secrets set --file secrets.env --wait-for-ack
```

The linked app and environment provide defaults; explicit `--app` and `--scope`
override them. Gregale validates the whole file before writing, rejects duplicate
keys, and reports malformed input with line numbers without displaying values.
`--file` cannot be combined with stdin, interactive input, or positional pairs.
A dry run makes no API requests. Keep the file out of version control.

## Waiting for recorded health

```sh
gregale app my-api health --wait --timeout 2m
```

Gregale polls recorded default-scope health every two seconds. Success requires
a `healthy` assessment whose evaluation time is not in the future and remains
within its positive validity window. This does not wake or restart the app or
prove every route and workload is healthy. Transient read failures are retried;
authorization and other permanent request errors stop the wait.

Human progress appears on stderr only when the status, phase, scope, freshness,
or summary changes. `--json` emits one final assessment on stdout. On timeout,
exit code 124 accompanies the last fetched assessment, if any, plus an error on
stderr; that observation may be stale. Cancellation exits 130. Ordinary reads
and history output remain unchanged. `--poll-interval` accepts 1s to 1h; timeout
must be positive. Both options require `--wait`, which excludes history options.

## Checking health in scripts

```sh
gregale app my-api health --check
```

`--check` makes one read of recorded health and exits 0 only when the assessment
is healthy, default-scope, and within its positive timestamp validity window.
Degraded, unhealthy, unknown, stale, future-dated, invalid-timestamp, and other-scope
assessments exit 1. API failures retain their existing error exit codes. The read
has a 30-second deadline and does not wake or restart the app.

The full assessment remains on stdout, including with `--json`; a failed check
also writes a diagnostic to stderr. Use the exit code rather than parsing the
assessment to gate a script. `--check` excludes wait, history, timeout, poll
interval, and history pagination options. Ordinary health reads are unchanged.

## Removing several secrets

```sh
gregale secrets unset OLD_TOKEN UNUSED_KEY --dry-run
gregale secrets unset OLD_TOKEN UNUSED_KEY --wait-for-ack
```

Keys are validated before any API request, deduplicated in input order, and
removed from the same selected scope. A dry run validates locally without API
requests. `--restart` requests one fresh restart after all removals succeed.
`--wait-for-ack` checks each durable revocation using one shared timeout starting
after all removals, including the optional restart request.

Removal is sequential, not atomic. Failure stops further removals, skips any
remaining restart/wait steps, and reports confirmed keys. A failed removal
response does not establish whether that removal happened. Review metadata
before retrying. Batch JSON receipts include requested keys, confirmed removed
keys, per-key revocation status, any unconfirmed key, and accepted restart ID;
on failure the receipt is on stdout and the error is on stderr. Single-key
receipts retain their existing format. Shell completion also supports later
key positions after regenerating the completion script.

## Fixing missing or malformed flag values

Human errors from the shared flag parser now include the expected value type
and a format example for durations, numbers, and booleans. For flags with
documented choices, a missing value lists those choices. Other documented
value-taking flags show their placeholder, such as `PATH` or `SLUG`.

For example, `gregale app my-api health --wait --poll-interval nope` explains
that the value must be a duration and shows `--poll-interval 2s`. Format examples
do not override command-specific limits. The guidance uses the active parser
and command documentation, never stored defaults or supplied values. JSON
errors and exit codes remain unchanged. Command-specific validation errors
continue to report their own constraints.

## Inspecting the latest deployment

To find failed deployments in history:

```sh
gregale deployments --app my-api --failed-only --all
```

`--failed-only` filters fetched history to deployments whose recorded status
is `failed`. It filters one page by default or all pages from the supplied
cursor with `--all`. Page size and cursors describe unfiltered history. Human
output includes exact deployment-detail commands and, for app-scoped reads,
deployment-log commands. Account-wide reads provide detail commands and suggest
`--app` for scoped logs. Next-page commands retain the app, filter, limit, and
wide-output option even when the current page has no failures. JSON preserves
the one-page envelope/cursors or the existing `--all` NDJSON format. Empty
results only describe fetched history and do not establish application health.
The command changes no deployments; ordinary output remains unchanged.

```sh
gregale deployment latest
gregale deployment wait latest --timeout 2m
```

Use a linked app or supply `--app SLUG`. `latest` selects the newest entry in
that app's deployment history across scopes, including failed or pending
deployments; it does not mean the currently serving deployment. Human output
identifies the selected ID, revision, scope, and recorded status on stderr.

The selection uses one history read and pins the resulting ID for the command,
so a later upload cannot change what a running wait observes. JSON output keeps
the existing deployment response format. Inspection bounds the latest lookup
and initial detail read to 30 seconds; waiting includes lookup in its existing
timeout. Empty history reports how to proceed. This selector is supported by
`deployment latest` and `deployment wait latest`; other deployment operations
continue to require their existing explicit references.

## Reading the newest job run

When `jobs wait` or a mutation with `--wait` (`jobs run`, `jobs retry`, or
`jobs replay-failed`) observes the exact run succeed, human output also prints
`jobs tasks JOB RUN_ID --artifacts-only`. The command preserves the resolved
run UUID and profile. No shortcut is shown for failed, cancelled, dead-letter,
active, unknown, or mismatched runs, or when waiting ends with an error.
This guidance requests no additional data and keeps wait receipts and exit
codes unchanged. A successful run may still have no recorded artifacts.

Human `jobs status` and `jobs runs` output for succeeded runs includes a
copyable `jobs tasks JOB RUN_ID --artifacts-only` command. History shows up to
three unique succeeded runs in returned order and counts any additional ones;
this also applies to `jobs runs --status succeeded`. Commands retain the exact
run UUID and active profile. Success alone does not establish that a run has
artifacts; discovery checks its current task manifests. No extra API requests
are made for guidance, and JSON output remains unchanged.

To find tasks that produced artifacts without checking indexes individually:

```sh
gregale jobs tasks nightly-report latest --artifacts-only
```

This pins the run, checks its Job/account identity, and scans all task pages
from `--offset` using `--limit` when supplied. It includes tasks whose current
recorded manifest contains artifacts. Missing/null manifests and valid empty
manifests are omitted; an invalid manifest fails before any results are printed.
With `--failed-only`, both filters apply. Human output shows managed/external
counts, artifact-listing commands, and bulk-download commands for tasks with
managed artifacts, retaining the run UUID and active profile. Suggested task
directories are created when missing and must contain no conflicting filenames. External
artifacts require their storage provider. JSON retains the ordinary task NDJSON
shape, filtered to matching tasks. Discovery downloads nothing, requests no
signed URLs, and does not verify stored bytes. Task outputs may change on retry.

To discover only tasks producing selected artifact names:

```sh
gregale jobs tasks nightly-report latest --artifacts-only --include '*.csv'
```

Repeatable `--include` requires `--artifacts-only` and uses the same case-sensitive
artifact-name glob matching as downloads: any matching pattern includes an
artifact. Patterns are validated before API calls. All task pages from the
starting offset are scanned; `--limit`, `--offset`, and `--failed-only` continue
to work. A task appears only when its valid recorded manifest has a matching
artifact, including external artifacts. Invalid manifests in considered tasks
still fail before results are printed, even if no artifact name would match.

Human counts reflect only matching managed/external artifacts. Listing and
download commands retain every quoted include pattern, the resolved UUID, and
profile. Listing therefore shows the same matching names counted by discovery.
JSON retains complete, unchanged task records (including the original manifest),
filtered to matching tasks. No matching tasks succeeds with empty
NDJSON or a human message explaining that active tasks may produce outputs later.
Discovery downloads nothing and creates no files or directories. Without filters,
existing discovery output and JSON remain unchanged.

To discover artifact names before downloading:

```sh
gregale jobs artifacts nightly-report latest 0
```

The read-only command resolves the run once, checks its Job/account identity,
and locates the explicit task index across task pages. The lookup and reads
share a 30-second deadline. It validates the recorded output manifest, then
lists artifact names, byte sizes, SHA-256 checksums, and managed/external
storage classification. Managed artifacts include copyable download commands
with the resolved UUID and active profile. Suggested destinations are new
files in the current directory; the download command never overwrites files.
External artifacts require access through their storage provider. Listing
does not request signed URLs, download files, or verify stored bytes.

Missing manifests and manifests with no artifacts have distinct human
messages. JSON returns `run_id`, `task_index`, `attempt`, `task_status`,
`has_manifest`, and an `artifacts` array of `name`, `size_bytes`, `sha256`,
and `managed`; URIs and signed URLs are omitted. Invalid manifests fail
before artifact guidance is displayed. Recorded outputs can change after
a task retry; direct download verifies the current managed result.

Artifact listing accepts the same repeatable name filters:

```sh
gregale jobs artifacts nightly-report latest 0 --include '*.csv' --include 'summary.json'
gregale jobs artifacts nightly-report RUN_ID 0 --attempt 2 --include '*.csv'
```

Matching uses case-sensitive name globs; any matching pattern includes the
artifact. Patterns are validated before API calls, and the complete recorded
manifest is validated before filtering. JSON includes `include` when filters
are provided and keeps its existing fields with a filtered `artifacts` array;
`has_manifest` describes the original manifest, even when no names match.
Human output distinguishes missing manifests, manifests with no artifacts, and
valid manifests whose names do not match. No matches succeeds without downloads
or file writes. Current managed-artifact commands refer to each exact matching
name, while `--attempt` remains historical metadata only and emits no download
commands. Without filters, existing listing JSON and behavior are unchanged.
Task discovery now preserves filters in both listing and download commands.

To inspect the recorded manifest from a specific retained terminal attempt:

```sh
gregale jobs artifacts nightly-report latest 0 --attempt 2
```

The command pins the run, checks its identity, and searches retained attempt
pages for that exact task and attempt. Missing, active, or expired attempts
fail without falling back to current output. JSON keeps the same shape and
identifies the selected attempt. This mode lists metadata only and omits all
download commands: historical downloads are not supported by the server,
and `artifact-download` always reads the current task output.

To run or continue a job while collecting only selected results:

```sh
gregale jobs run nightly-report --tasks 10 --out-dir ./results --include '*.csv'
gregale jobs wait nightly-report RUN_ID --out-dir ./results --include '*.csv' --include 'summary.json'
```

Repeatable `--include` requires `--out-dir` and uses the same case-sensitive
artifact-name glob matching as bulk downloads. Any pattern match includes the
artifact. Invalid patterns and missing output-directory options fail before
API calls, including before job submission. Filtering is local and is not sent
in the job request. Unmatched artifacts are reported as `skipped_filter` and do
not cause local conflicts or directory writes; no matches succeeds without
downloading files. All recorded manifests are still validated.

The combined receipt includes `include` even if waiting fails, and the nested
artifact receipt includes the same patterns after collection starts. Commands
to continue waiting or downloading retain every quoted pattern, the resolved
UUID, profile, and directory. `jobs wait --out-dir` continues to verify and skip
matching existing files, only for selected artifacts. Commands without filters
retain their existing JSON shape and behavior.

To finish waiting and collect an existing run without submitting another:

```sh
gregale jobs wait nightly-report RUN_ID --out-dir ./results
```

`latest` is also accepted and resolves once before waiting; every subsequent read
and recovery command uses that UUID. The command checks run/Job/account identity,
waits for success, then collects whole-run artifacts using resume verification:
matching existing regular files are verified and skipped, missing files are
downloaded, and mismatches or symlinks fail without overwriting anything. Missing
output directories are created after validation. Failed/cancelled/dead-letter
runs, timeout, or interruption do not start collection. `--timeout` and
`--poll-interval` control waiting; collection has a separate five-minute deadline.
`--out-dir` is available for explicit targets and is not supported with
`--interactive`. Destination validation happens before API requests.

JSON emits the same combined wait/download receipt as `jobs run --out-dir`.
Wait-phase errors preserve the last observed run (or just its selected identity
if no read succeeded) and provide a command including the output directory.
Collection errors provide a download-resume command. `jobs run --out-dir`
recovery commands now also include `jobs wait ... --out-dir`, so an interrupted
wait can finish collection in one step. Plain `jobs wait` output and exit codes
remain unchanged. No submission or retry request is made by this command.

To submit once, wait, and collect results with one command:

```sh
gregale jobs run nightly-report --tasks 10 --out-dir ./results
```

`--out-dir` implies `--wait` and accepts `--wait-timeout` and `--poll-interval`.
The destination path is inspected without creating directories before submission;
invalid paths and wait options fail before any POST. After one accepted submission,
Gregale waits for the exact returned run UUID. Only a succeeded run starts whole-run
artifact collection into `task-INDEX/` directories, using all the usual destination,
manifest, size, and SHA-256 checks. The wait defaults to ten minutes; artifact
collection has a separate five-minute deadline. A valid empty result succeeds
without creating directories. External artifacts are reported but not downloaded.

Job failure, wait timeout/interruption, and collection failure return nonzero.
Receipts preserve the accepted run and offer commands to wait, inspect, or collect
that exact run without submitting again. Failed runs can be collected separately
if their recorded outputs are useful. Download failures preserve verified files
and provide a bulk `--resume` command. JSON emits one combined receipt containing
`run`, `exit_code`, `phase` (`wait` or `download`), `download_command`, an optional
`resume_command`/`error`, and nested `artifacts` when collection produced a receipt.
Ordinary run and `--wait` output without `--out-dir` remains unchanged. The output
path is local and is not included in the job submission request.

Bulk downloads (`--all` and `--all-tasks`) create a missing `--out-dir`,
including missing parent directories, after validating the selected run,
manifest(s), and artifact destinations. New directories use permissions 0750
(subject to umask). Existing path components must be real directories, never
files or symlinks; a destination that appears during creation causes failure
rather than being silently adopted. No directory is created for runs/tasks with
no managed artifacts. Invalid metadata and conflicts cause no directory writes.
`--dry-run` prints planned directories and includes `directories_to_create`
in JSON when applicable, without creating them. Preview cannot guarantee write
permissions. Failed transfers may leave created directories and verified files.
Single-artifact `--out` retains its existing parent-directory requirement.

To omit unwanted artifact names:

```sh
gregale jobs artifact-download nightly-report latest --all-tasks --include '*.csv' --exclude 'debug-*.csv' --out-dir ./results
gregale jobs run nightly-report --tasks 10 --out-dir ./results --exclude 'debug-*'
gregale jobs wait nightly-report RUN_ID --out-dir ./results --exclude 'debug-*'
gregale jobs tasks nightly-report latest --artifacts-only --exclude 'debug-*'
gregale jobs artifacts nightly-report RUN_ID 0 --attempt 2 --exclude 'debug-*'
```

Repeatable `--exclude` uses the same case-sensitive artifact-name globs as
`--include`. Inclusion defaults to all names when no include patterns are given;
otherwise any include match selects a name. Any exclude match then removes it,
even if an include pattern also matched. Exclusion patterns are validated before
API calls or submission. Bulk downloads require `--all` or `--all-tasks`, automatic
collection requires `--out-dir`, and task discovery requires `--artifacts-only`.
Single-artifact downloads reject exclusion patterns.

Excluded artifacts are `skipped_filter` in download/preview receipts and are omitted
from listing and task-discovery counts. They are not inspected locally, verified,
or downloaded, do not cause destination conflicts, and do not contribute to byte
totals or directory creation. All considered manifests are still validated. Both
filter lists are preserved in JSON (`include`/`exclude` when provided), listing,
download, wait, and resume commands. Historical listings remain metadata only.
No selected artifacts succeeds with no downloads or directory writes. Without
exclusion patterns, existing output and JSON retain their previous behavior.

To collect only selected artifact names across tasks:

```sh
gregale jobs artifact-download nightly-report latest --all-tasks --include '*.csv' --include 'summary.json' --out-dir ./results
```

Repeatable `--include` is available with `--all` and `--all-tasks`, including
`--dry-run` and `--resume`. An artifact is included when its name matches any
pattern; matching is case-sensitive and supports `*`, `?`, and character classes.
Patterns apply to names, not URIs or paths. Quote patterns to prevent shell
expansion. Empty, malformed, slash-containing, or NUL-containing patterns fail
before API requests. Single-artifact downloads reject this option.

Unmatched artifacts are reported as `skipped_filter` and are not inspected,
verified, or downloaded. Matched external artifacts remain `skipped_external`.
All recorded manifests are still validated before guidance/transfers. Only
matching managed artifacts participate in destination conflicts, byte totals,
and directory creation. No matches succeeds with no download or directory
writes. Filtered receipts include the `include` patterns and `skipped_filter`
counts; preview and recovery commands retain every quoted pattern and the
resolved UUID/profile. Downloads without `--include` retain their existing
behavior and JSON shape. `jobs run`/`jobs wait` automatic collection also accepts these filters
when `--out-dir` is provided.

To download current results across an entire run:

```sh
gregale jobs artifact-download nightly-report latest --all-tasks --out-dir ./results
```

Gregale resolves one run, verifies Job/account
identity, and scans every task page. It validates all recorded manifests and
preflights all managed destinations before transferring anything. Each task's
files go into `task-INDEX/` under the parent; missing task directories are created
only during a real download. Equal artifact names in different tasks therefore
do not collide. Existing task directories must be real directories, never
symlinks. Existing artifact files are never overwritten. Missing manifests and
valid empty manifests are reported per task; invalid manifests abort discovery.
External artifacts are reported but not downloaded. Active tasks may publish
outputs later; this command reads the current recorded manifests once.

Add `--dry-run` to preview paths, conflicts, and bytes without creating
directories, writing files, or requesting signed URLs. Add `--resume` with an
explicit run UUID to verify and skip matching existing files, downloading only
missing ones. `--all-tasks` omits the task index and cannot be combined with
`--all` or `--out`. Receipt JSON includes Job/run, `dry_run`, `ready`, per-task
index/attempt/status/manifest status and artifact results, summary counts,
and planned `download_bytes`. Preview includes `download_command`; incomplete
real downloads include `resume_command`. Commands retain the resolved UUID,
absolute parent directory, and profile. Conflicts prevent all transfers;
failures return nonzero and preserve previously verified files and directories.
Transfers retain size/SHA-256 verification and a five-minute overall deadline.
Historical-attempt downloads remain unsupported.

To preview a bulk download before writing files:

```sh
gregale jobs artifact-download nightly-report latest 0 --all --out-dir ./results --dry-run
```

Preview accepts missing destination directories and shows every artifact's size,
managed destination path, and status: `would_download`, `verified_existing`
(with `--resume`), `skipped_external`, or `conflict`. It reports all destination
conflicts rather than stopping at the first. The summary counts each status and
sums bytes for files that would download, excluding conflicts, external files,
and verified existing files. Conflicts return nonzero; an empty valid manifest
returns zero. Preview requests no signed URLs, downloads no bytes, and writes
no files. `--resume --dry-run` reads and verifies existing local files and still
requires an explicit run UUID. Ordinary dry-run can resolve `latest` once.

JSON includes `dry_run`, the selected Job/run/task/attempt, `ready`, `artifacts`,
`summary`, and `download_command`. Human output also prints that POSIX command,
retaining the resolved UUID, absolute directory, profile, and resume option,
with `--dry-run` removed. Storage URIs and signed URLs are omitted. The preview
checks recorded metadata and local destinations; it does not guarantee directory
write permissions or remote availability. Downloads recheck destinations and
checksums, since local files and task outputs may change after the preview.

To download all current managed artifacts into a directory:

```sh
gregale jobs artifact-download nightly-report latest 0 --all --out-dir ./results
```

This resolves the run once and validates the task manifest. All managed
filenames and destinations are checked before transfers; existing files and
symlinks cause the command to stop without downloading anything. Each download
must match the discovered size and SHA-256, so changed metadata fails rather
than silently downloading a newer result. Files are saved sequentially without
overwriting. External artifacts are reported as `skipped_external`.

The receipt identifies the run, task, attempt, and each artifact as `saved`,
`failed`, `skipped_external`, or `not_attempted` after interruption. It omits
storage URIs and signed URLs. Receipts also include a `summary` object counting
`saved`, `verified_existing`, `failed`, `skipped_external`, and `not_attempted`.
When transfers fail or are interrupted, human output prints a copyable POSIX
resume command and JSON includes `resume_command`. It retains the selected run
UUID, task index, absolute destination directory, and active profile. Address
reported failures before resuming; mismatched existing files still fail safely.
Preflight failures do not produce a transfer receipt or resume command.
Successful receipts omit `resume_command`. Failures return nonzero; earlier verified files
remain saved. A valid empty manifest succeeds without downloads; a missing or
invalid manifest fails. The overall deadline is five minutes. Retry individual
failed artifacts into new files, or resume the bulk download:

```sh
gregale jobs artifact-download nightly-report RUN_ID 0 --all --out-dir ./results --resume
```

Resume requires the explicit UUID from the earlier receipt (`latest` is rejected).
Before transfers, every existing managed destination must be a regular file
matching the discovered size and SHA-256. Matching files are rechecked before
being reported as `verified_existing`, without requesting their signed URLs.
Missing files are downloaded normally. Mismatched files, symlinks, directories,
and unreadable files stop preflight without downloading or overwriting anything.
Without `--resume`, existing destinations still cause failure. Verification
uses the current task manifest, so a retry that changes outputs can prevent
resumption even within the same run. Historical downloads are unsupported.

To download a managed job result directly:

```sh
gregale jobs artifact-download nightly-report latest 0 report.csv --out report.csv
```

The command resolves `latest` once (explicit UUIDs also work), obtains the
named task artifact's signed GET request, and downloads it without adding
the Gregale API bearer token. It verifies the artifact name, byte size, and
SHA-256 before publishing the local file. `--out` must name a new file in an
existing directory; files and symlinks are never overwritten. Data streams
through a private temporary file in that directory, removed on failure.
The final file appears only after verification succeeds. No file is downloaded
from a redirect. API lookup has a 30-second deadline and the full operation
has a five-minute deadline; Ctrl-C stops it. A failed transfer reports no
signed URL or response body. It does not search older runs for an artifact.

Human output identifies the saved path, selected UUID, task, and size. JSON
returns `job`, `run_id`, `task_index`, `artifact`, `path`, `size_bytes`, and
`sha256`; it omits the signed URL. Existing `artifact-url` output remains
unchanged. The command reads the remote result and writes only the requested
local file; it does not change the Job run.

For a one-time run summary:

```sh
gregale jobs status nightly-report latest
gregale --json jobs status nightly-report RUN_ID
```

`jobs status` shows the recorded aggregate state, task counts, dead-letter
count, parallelism, available timestamps, and source run for a replay. It
validates the Job and selected run/account identities. Latest selection
resolves once; the Job lookup, selection, and run read share a 30-second
deadline. Human output includes an exact-UUID wait command for active runs,
failed-task inspection for failed/dead-letter runs, and a command to inspect
all tasks. It omits environment snapshots and command overrides. JSON returns
the existing run object, including its normal API fields. Reading a failed
run exits 0 when the read succeeds; use `jobs wait` to assess completion for
automation. No run is submitted, retried, or cancelled.

For a one-time success check in scripts:

```sh
gregale jobs status nightly-report RUN_ID --check
```

`--check` displays the same run summary and exits 0 only when its recorded
aggregate status is `succeeded`. Queued, running, failed, cancelled,
dead-letter, and unknown statuses exit 1 with an explanation on stderr.
JSON still emits the run object on stdout; a failed check emits the normal
JSON error on stderr. API errors retain their usual codes. The command reads
status once without polling, and ordinary status reads retain exit 0 when
the read succeeds. `latest` works too, but explicit UUIDs keep CI checks tied
to the intended run. A successful check describes the run, not app health.

To inspect a complete task list:

```sh
gregale jobs tasks nightly-report latest --all
```

`--all` reads every task page from the selected starting `--offset` (default
0), using the resolved run UUID throughout. `--limit N` sets page size
(1..200); traversal defaults to 200, while an ordinary one-page read retains
the server default. Each traversal read has a 30-second deadline, Ctrl-C
stops it, and results are buffered so page failures emit no partial list.
Human output retains failure-log commands; a one-page result with more tasks
includes a next-page command containing the UUID and any explicit limit.
JSON retains NDJSON. `--failed-only` still traverses pages automatically and
now also respects the supplied limit and starting offset. Task states may
change during traversal; results are not an atomic snapshot. Without these
options, the request and JSON format remain unchanged.

To read all retained attempts for one task:

```sh
gregale jobs attempts nightly-report latest 0 --all
```

Add `--failed-only` to show only retained attempts with status `failed`,
`timeout`, or `oom`, keeping their copyable log commands. Cancelled and
successful attempts are excluded. The filter applies to fetched history:
without `--all`, it filters one page; with `--all`, it filters all pages from
the starting offset. An empty filtered page may still have more history, and
its next-page command retains `--failed-only`. Human output explains when no
matching attempts were found. JSON keeps its wrapper and pagination metadata
while filtering only `attempts`. Page size and offsets describe unfiltered
history, and absent failures do not establish that the task succeeded.

`--limit N` controls history page size (1..200, server default 50), and
`--offset N` selects a non-negative starting offset. `--all` reads subsequent
pages for the same resolved run UUID. Each page has a 30-second read deadline,
and Ctrl-C stops traversal. Results are buffered; a page failure or invalid
continuation emits no partial history. Human output retains per-attempt log
commands; a one-page read shows a next-page command with the exact UUID.
JSON keeps the `attempts`, `limit`, `offset`, and `next_offset` wrapper; with
`--all`, `attempts` contains all fetched rows, `limit` is the page size,
`offset` is the starting offset, and `next_offset` is -1 on completion.
Empty retained history does not mean the task never ran; active or expired
attempts may be absent. The command does not retry tasks. Ordinary reads
without pagination options preserve their JSON request and output formats.

To compare output from a particular retained attempt:

```sh
gregale jobs logs nightly-report latest 0 --attempt 1 --max-bytes 65536
```

`--attempt N` requires a positive attempt number and reads retained terminal
attempt history instead of the task's current logs. It scans history pages
until it finds the exact run, task index, and attempt; selection and history
reads share a 30-second deadline. Missing history reports an attempts command;
an attempt may still be active or no longer retained. No retry is submitted.

Human stderr identifies the selected attempt; stdout contains its retained
output. JSON contains `task_status`, `log_content`, `truncated`, `max_bytes`,
`run_id`, `task_index`, and `attempt`. `--max-bytes` bounds the displayed tail
(default 64 KiB, maximum 1 MiB), preserving UTF-8 boundaries. Truncation is
reported when either retained history or the local limit omits output;
increasing the local limit cannot recover output the server did not retain.
An empty retained log uses the existing no-output message. These options apply
to the explicit command; ordinary logs without `--attempt` stay unchanged.

To find runs with a particular outcome:

```sh
gregale jobs runs nightly-report --status failed --all
```

`--status` accepts `queued`, `running`, `succeeded`, `failed`, `cancelled`, or
`dead_letter`. Gregale filters fetched history locally: without `--all`, it
filters one history page; with `--all`, it scans pages from the given offset.
`--limit` remains the history page size, not a matching-result count. A page
with no matches can still have more history, and its next-page command keeps
the status and limit. JSON retains NDJSON and contains only matching runs.
Human failed/dead-letter results include `jobs tasks JOB RUN --failed-only`
commands with exact run UUIDs. Empty output only means no matches in fetched
history; run states can change. Traversal buffers results, so page failures
emit no partial list. Omitting `--status` preserves the existing history read
and JSON output. Ordinary human history now adds exact-UUID follow-up commands:
failed/dead-letter runs use `jobs tasks JOB RUN_ID --failed-only`, while
queued/running runs use `jobs wait JOB RUN_ID`. Commands retain the active
profile and cover up to three distinct matching runs in returned order, with
a count for additional runs. Successful, cancelled, unknown-status, and
invalid-ID rows receive no guidance. No extra reads or mutations are sent.

To focus on task failures without interactive browsing:

```sh
gregale jobs tasks nightly-report latest --failed-only
```

`--failed-only` scans all task pages for the selected run and shows only
`failed`, `timeout`, and `oom` tasks, with the existing scoped log commands.
Human failure guidance includes both current-log and retained-attempt-history
commands for failed, timed-out, and out-of-memory tasks. Commands use the
selected UUID and active profile. Guidance shows at most three task indexes
in ascending order, with a count when more failures are listed. JSON remains
unchanged; successful, active, and cancelled tasks do not receive this guidance.
It excludes cancelled, queued, claimed, and successful tasks. Explicit UUIDs
also work. Human output explains when no matching failures are found; JSON
keeps NDJSON and emits no rows when none match. Results are buffered, so a
failed page emits no partial list. Each page read has a 30-second deadline;
Ctrl-C stops the traversal. This is a read of changing task states, not an
atomic snapshot or a claim that the run succeeded. Without the flag, the
existing one-page read and output remain unchanged.

```sh
gregale jobs tasks nightly-report latest
gregale jobs logs nightly-report latest 0 --max-bytes 65536
gregale jobs attempts nightly-report latest 0
gregale jobs artifact-url nightly-report latest 0 report.csv
gregale jobs wait nightly-report latest --timeout 10m
```

`latest` selects the newest run in the named job's history, including pending
and failed runs. Human output identifies the run and recorded status on stderr.
Each command resolves once and reads that exact ID; a newer run submitted during
the read does not change its target. Separate commands resolve independently.
Use the printed UUID when several commands must inspect the same run.

For tasks, logs, attempts, and artifact URLs, the history lookup and selected read share a
30-second deadline. Empty history
reports a configuration-inspection command before suggesting a new submission.
Task indexes remain explicit and zero-based. Log size limits and ordinary UUID
reads retain their existing behavior; JSON output keeps its existing shape.
`attempts` reads retained outcomes for the explicit task index, including
earlier attempts whose task was subsequently retried. It uses the selected
run UUID and does not retry the task or change the run. Invalid task indexes
or an invalid Job name for `latest` stop before the history lookup.
Attempts with valid recorded manifests containing artifacts also include a
copyable `jobs artifacts JOB RUN_ID TASK_INDEX --attempt N` command. It uses
the resolved run UUID and active profile to inspect metadata from that exact
attempt. Missing, empty, or invalid manifests do not produce artifact guidance.
No extra API requests are made, and JSON output remains unchanged. Historical
artifact downloads remain unsupported.

Each retained attempt in human output includes a copyable
`jobs logs JOB RUN_ID TASK_INDEX --attempt N` command when its run and task
identity match the request. Commands use the resolved UUID and active profile,
so later submissions cannot change which run's output you inspect. JSON
attempt-history output remains unchanged.
`artifact-url` retrieves the named managed artifact's signed download URL for
the explicit task index. It selects the newest run, including unfinished or
failed runs, rather than searching older runs for an available artifact.
The server retains its artifact verification and access checks. Gregale does
not download the file or change the run. A missing artifact name, invalid
task index, or invalid Job name for `latest` stops before lookup; human URL,
checksum, and size output and the existing JSON response stay unchanged.
Cancellation, retries, and replay continue to require explicit run IDs.

`jobs wait JOB latest` reads the Job configuration and resolves the newest run
once within a shared 30-second selection deadline. It then follows the selected
UUID using the normal wait timeout (default 10m); newer runs do not change its
target. Status reads check the run, Job, and account IDs. The human resume
command and JSON `resume_command` contain the selected UUID, so you can
continue watching the same run after timeout or interruption. Existing wait
exit codes, JSON receipts, and explicit UUID behavior are unchanged. Waiting
does not submit or cancel runs.

## Using the linked app for workflows

Inside a checkout linked with an app, these commands use that app by default:

```sh
gregale workflows list
gregale workflows run nightly-report
```

An explicit `--app SLUG` overrides the checkout context, including a missing or
malformed context file. Without an explicit app, missing, ambiguous, or malformed
context stops the command locally with instructions to pass `--app` or repair
the link. Context discovery works from nested directories.

Before a human-mode run submission, stderr shows the selected connection profile,
app, and whether the app came from the flag or linked project. It displays no
input payload, credentials, or idempotency key. Workflow APIs target the app;
a linked environment does not add an environment selector. List filters, run
idempotency, and JSON responses retain their existing behavior.

## Loading workflow input from a file

```sh
gregale workflows run nightly-report --input-file input.json
gregale workflows run nightly-report --input-file -
```

`--input-file -` reads stdin until EOF. File paths are relative to the current
directory. Input must contain exactly one valid JSON value and fit within
1 MiB, including whitespace, before Gregale submits a run. Malformed, empty,
missing, or oversized input stops locally. `--input` and `--input-file` cannot
be combined, even if the explicitly supplied inline value is `{}`. Omitting
both retains the default `{}` input. Inline input receives the same size check.

The linked app, explicit `--app` override, and `--idempotency-key` work as before.
Loading diagnostics and target previews do not display the input payload.
Successful command responses retain their existing format; workflows may
return output according to their own definitions.

## Waiting for a workflow run

```sh
gregale workflows wait RUN_ID --timeout 5m
```

The command polls that exact run every two seconds and succeeds only when the
run reports `succeeded` without a cancellation timestamp. Failed, dead, canceled,
and unrecognized states exit 1; API errors retain their normal exit codes.
Pending, running, and awaiting-event states continue waiting. The command
only reads status; it does not resume, retry, cancel, or send workflow events.

Human progress on stderr changes when status, step, resume count, or availability
changes. The final run details appear on stdout. JSON mode emits one final run
object with the existing status response shape. Transient read failures are
retried; permanent request errors stop the wait. Each read has a 30-second
limit within the overall deadline.

Timeout exits 124 and returns the last observation, if any, plus a resume-wait
command on stderr. The workflow may still be active. Ctrl-C exits 130 and
leaves the run untouched. `--timeout` must be positive; `--poll-interval` accepts
1s to 1h. Accepted-run receipts also show the wait command.

## Starting and waiting in one command

```sh
gregale workflows run nightly-report --input-file input.json --wait --timeout 5m
```

Gregale submits once and follows the ID returned by the server using the same
logic as `workflows wait`. The timeout starts after acceptance; it does not
bound the initial submission. `--poll-interval` defaults to 2s and accepts 1s
to 1h. Explicit wait timing options require `--wait`. Linked-app defaults,
input files/stdin, and idempotency keys continue to work.

Human output shows acceptance on stderr, then status changes and final run
details. JSON mode returns one final run object, or the last known run object
if waiting stops. Success exits 0, terminal failure exits 1, timeout exits 124,
and Ctrl-C while waiting exits 130. API failures retain their normal codes.
On timeout, cancellation, or a permanent read failure, the accepted ID remains
available and recovery guidance resumes `workflows wait` without another
submission. A malformed acceptance response stops with instructions to inspect
run history before submitting again. Without `--wait`, output stays unchanged.

## Inspecting the newest workflow run

For a one-time workflow success check:

```sh
gregale workflows status RUN_ID --check
gregale workflows status latest --workflow-name billing --check
```

`--check` reads once and shows the same status details, exiting 0 only for
`succeeded` with no cancellation timestamp. Active, failed, dead, unknown,
or cancelled runs exit 1 with an explanation on stderr. JSON retains the run
object on stdout and uses the normal JSON error on stderr for a failed check.
API errors retain their usual codes. Selection and status share a 30-second
deadline, and check mode validates that the returned ID matches the selected
run. Explicit UUIDs keep script checks tied to the intended run. Without
`--check`, successful status reads retain exit 0. Check applies to the explicit
read command, not interactive selection; it does not wait or change the run.

Human `workflows status` output includes an exact-UUID wait command for active
runs, with a brief explanation for queued, executing, or event-waiting states.
Waiting observes the selected run and does not send events. Guidance retains
the active profile and is omitted for cancelled, terminal, unknown-state, or
invalid-ID responses. Existing failure guidance remains available. JSON and
API requests are unchanged. The same active-state explanations appear beside
wait commands in workflow history.

To focus on failed steps without scanning successful or pending steps:

```sh
gregale workflows steps latest --workflow-name billing --failed-only
```

`--failed-only` includes only steps whose recorded status is `failed`.
Explicit UUIDs also work. Human output includes commands for run diagnostics
and each failed step's attempts, using the selected UUID. These commands
inspect evidence; they do not retry or resume the workflow. When no steps
match, human output says so without claiming the run succeeded. JSON retains
NDJSON and emits no rows for an empty result. Step states may change after
the read. Without the flag, existing step output remains unchanged.

```sh
gregale workflows status latest --workflow-name nightly-report
gregale workflows steps latest --workflow-name nightly-report
gregale workflows diagnose latest --workflow-name nightly-report
gregale workflows attempts latest charge-card --workflow-name billing
gregale workflows resumes latest --workflow-name billing
```

These reads require an exact workflow name and use the linked app unless
`--app SLUG` overrides it. The server filters the history lookup by workflow
name; Gregale resolves the newest matching run once and reads that exact ID.
Latest includes unfinished and failed runs. Human stderr identifies the app,
workflow, run ID, and recorded status; JSON retains its existing shape.
`diagnose` explains queue or failure reasons and previews recovery eligibility
for the selected run without changing it. A diagnostics response identifying
a different run is rejected before displaying its recovery preview.
`attempts` lists retained retry attempts and managed-effect delivery status
for the explicitly named step. It reads the selected run without retrying the
step or changing the workflow. A missing step name stops before any lookup.
Add `--failed-only` to show only attempts whose recorded status is `failed`,
with the existing HTTP status, start/finish times, and next-attempt details.
For example: `gregale workflows attempts latest charge-card --workflow-name
billing --failed-only`. Explicit UUIDs also work. Human output explains when
no failed attempts match; JSON retains NDJSON and emits no rows for an empty
result. An empty result does not establish that the step or workflow succeeded;
attempt states and retained history may change. Ordinary output without the
flag remains unchanged.
`resumes` reads continuation history for the selected run. It does not resume
the workflow or submit any other change. Explicit UUID output and JSON
formats remain unchanged.

Lookup and detail reads share a 30-second deadline. Empty history gives a
filtered history command before another submission. Each command resolves
independently; use the printed UUID to inspect the same run across commands.
Explicit UUID reads need no app or workflow selectors. Retry, resume,
and cancellation continue to use explicit run IDs.

To wait for the newest matching workflow run without copying its UUID:

```sh
gregale workflows wait latest --workflow-name billing --timeout 5m
```

Selection uses the linked app or `--app SLUG`, requires an exact workflow name,
and resolves once within a 30-second lookup deadline. Waiting then uses the
normal timeout and follows only the selected UUID, even if newer runs appear.
Timeout and interruption provide a wait command containing that UUID. JSON
retains the final or last observed run object on stdout and wait errors on
stderr. Exit codes remain 0 for success, 1 for terminal failure, 124 for
timeout, and 130 for interruption. No run is submitted or cancelled.

## Reading all workflow history pages

Human history also includes exact-UUID `workflows wait` commands for pending,
running, and event-waiting runs. Guidance covers up to three distinct active
runs in returned order, with a count for additional runs. Event-waiting guidance
explains that waiting only observes the run and does not send an event or
advance it. Cancelled runs, successful/unknown states, and invalid UUIDs receive
no active-run guidance. Commands retain the active profile. This adds no API
reads or mutations, and JSON remains unchanged.

Human workflow history includes troubleshooting commands for runs recorded
as `failed` or `dead`: `workflows diagnose RUN_ID` and
`workflows steps RUN_ID --failed-only`. Commands use exact UUIDs and the active
profile, so newer runs cannot change the inspection target. Guidance covers
up to three distinct failed runs in returned order and reports how many more
are listed. Successful, pending, running, and event-waiting runs receive no
failure guidance. This adds no API reads or mutations; JSON remains unchanged.

```sh
gregale workflows list --workflow-name nightly-report --all
```

`--all` walks matching pages starting at `--offset` (default 0), with `--limit`
controlling page size. Every request keeps the selected app, workflow name,
status, and date filters. Ordinary human listings show a copyable next-page
command with the same filters when more results are reported. JSON remains
one run object per line and includes no pagination instructions.

Traversal buffers results so a failed page does not emit a partial list. Each
read has a 30-second deadline; Ctrl-C stops traversal with exit 130. The existing
1,000-page guard bounds traversal. Repeated run IDs are suppressed with `--all`.
Offset history is not an atomic snapshot: runs arriving or changing status
while pages are read can shift the history. Narrow creation-time filters help
keep a historical query bounded.

## Loading workflow event payloads

```sh
gregale workflows events RUN_ID approved --payload-file approval.json
gregale workflows events RUN_ID approved --payload-file -
```

`--payload-file -` reads stdin until EOF. Files use paths relative to the current
directory and must contain one valid JSON value. Empty, malformed, missing,
oversized, or conflicting input stops locally before sending. Choose either
`--payload` or `--payload-file`; omitting both retains `{}`. The existing
`workflows events send` spelling supports both options too.

The event request must fit within 1 MiB, including the event name and JSON
envelope; Gregale also bounds raw file/stdin input to 1 MiB. Inline payloads
receive the same validation. Loading diagnostics do not display the payload.
The run ID stays explicit, and successful output keeps its existing format.

## Resuming and waiting in one command

```sh
gregale workflows resume RUN_ID --expected-resume-count 1 --wait --timeout 5m
```

The command sends one resume request, retaining the expected resume-count
check and optional idempotency key, then follows the exact accepted run with
`workflows wait` logic. The deadline starts after resume acceptance. Explicit
`--timeout` and `--poll-interval` require `--wait`; timeout must be positive and
the poll interval must be 1s to 1h (default 2s).

Human output identifies the accepted resume before showing status changes.
JSON returns one final run object, or the last observation when waiting stops.
Success exits 0, terminal failure exits 1, timeout exits 124, and Ctrl-C while
waiting exits 130. API errors retain their usual codes. Timeout, cancellation,
and permanent read failures preserve the accepted run and a wait command for
continuing without another resume request. A mismatched acceptance response
stops and directs you to inspect the requested run and resume history.
Without `--wait`, existing resume output and behavior are unchanged.

## Retrying and waiting in one command

```sh
gregale workflows retry RUN_ID STEP_NAME --wait --timeout 5m
```

With an explicit run ID and step name, Gregale sends one retry request and
then follows that exact run using the existing workflow wait logic. Server
retry eligibility checks still apply. The deadline starts after acceptance;
explicit `--timeout` and `--poll-interval` require `--wait`. Timeout must be
positive, and the poll interval must be 1s to 1h (default 2s).

Human output identifies the accepted run and step before status updates.
JSON emits one final run object, or the last observation if waiting stops.
Success exits 0, terminal failure exits 1, timeout exits 124, and Ctrl-C while
waiting exits 130. API errors retain their usual codes. Timeout, cancellation,
and permanent read failures provide a command to continue waiting without
retrying again. A mismatched acceptance response stops and directs you to
inspect the requested run and step attempts before repeating the retry.
Without `--wait`, existing retry output is unchanged. These wait options
apply to the explicit command, not interactive selection.

## Submitting and waiting for a Job run

To retry one task and wait for its run:

```sh
gregale jobs retry nightly-report RUN_ID 0 --wait --wait-timeout 10m
```

The explicit command submits one task retry, checks that the response identifies
the requested run and task index, and follows that same run with `jobs wait`
logic. Waiting assesses the whole run; another failed task may still make it
unsuccessful. Server retry eligibility and budget checks remain in effect.
`--wait-timeout` defaults to 10m and must be positive; `--poll-interval`
defaults to 2s and must be 1s to 1h. Explicit timing options require `--wait`.
The wait deadline starts after acceptance. JSON uses the job wait receipt;
timeout, interruption, or read failure preserves the last observation or
acceptance and a command to keep watching without retrying again. Success
exits 0, terminal failure/read failure exits 1, timeout exits 124, and Ctrl-C
while waiting exits 130. Mismatched receipts direct you to inspect the exact
run before repeating the retry. Ordinary output without `--wait` stays
unchanged; these options apply to the explicit command.

To submit a recovery run and follow it in one command:

```sh
gregale jobs replay-failed nightly-report SOURCE_RUN_ID --wait --wait-timeout 10m
```

The explicit command submits one replay request and waits for the newly
accepted recovery run, using the same receipts and exit codes as
`jobs run --wait`. It follows the recovery UUID, not the source run.
`--wait-timeout` defaults to 10m and must be positive; `--poll-interval`
defaults to 2s and must be 1s to 1h. Explicit timing options require `--wait`.
Timeout, interruption, and read failure preserve the accepted recovery run
and a command to continue waiting without replaying again. If the response
identifies the source run or lacks a complete recovery identity, inspect run
history before repeating the replay. Server replay eligibility checks still
apply. Ordinary output without `--wait` remains unchanged; wait options
apply to the explicit command, not interactive selection.

```sh
gregale jobs run nightly-report --tasks 10 --wait --wait-timeout 10m
```

The explicit command submits once and follows the exact accepted run using
`jobs wait` logic. Existing task, input, environment, and execution overrides
still apply. `--timeout` remains the per-task timeout in seconds;
`--wait-timeout` is the local wait duration after acceptance (default 10m).
Explicit `--wait-timeout` and `--poll-interval` require `--wait`; the wait
timeout must be positive and the interval must be 1s to 1h (default 2s).

Human stderr identifies the accepted run and a command to continue watching.
JSON uses the `jobs wait` receipt shape: `run`, `exit_code`, `resume_command`,
and an optional `error`. If no status read succeeds, `run` retains the
acceptance response; otherwise it contains the last observation. Success
exits 0, failed/cancelled/dead-letter runs or read failures exit 1, timeout
exits 124, and Ctrl-C while waiting exits 130. Stopping the wait does not
cancel or resubmit the run. Reads verify its run, Job, and account IDs against
the acceptance receipt. An incomplete receipt directs you to run history
before submitting again. Without `--wait`, existing output is unchanged.
Wait options apply to the explicit command, not interactive selection.

### Collect Job results with one command

```bash
gregale jobs results nightly-report
gregale jobs results nightly-report latest --out-dir ./results
gregale jobs results nightly-report latest --out-dir ./results --include '*.csv' --exclude 'debug-*'
gregale jobs results nightly-report latest --out-dir ./results --dry-run
```

`jobs results JOB` defaults to `latest`. Without `--out-dir`, it saves under `./results/JOB/RUN_ID/`, keeping different runs separate. Explicit run IDs and destinations remain available; an explicitly empty destination is rejected. The destination is displayed before collection and included as `output_directory` in JSON collection/whole-run receipts. Preview reports it without creating directories.

`jobs results` resolves `latest` once, waits for that exact run to succeed, then collects managed artifacts into `task-N/` directories. It verifies matching existing files and skips them, without overwriting conflicting files or submitting a run. Missing output directories are created only when collection needs them. Failed runs do not start automatic collection. Recovery commands retain the run UUID, destination, profile, and filters.

`--dry-run` previews the artifacts currently recorded, including local conflicts and files that would be downloaded. It does not wait, request download URLs, create directories, or write files; an active run may record more outputs later. `--timeout` (default 10m) and `--poll-interval` (default 2s) control waiting; collection uses its existing separate five-minute deadline. Exclusions take precedence over includes. JSON uses the existing combined wait/collection receipt, or the whole-run preview receipt for `--dry-run`.

### Start a Job and collect its results

```bash
gregale jobs run nightly-report --tasks 1 --results
gregale jobs run nightly-report --tasks 1 --results --include '*.csv' --exclude 'debug-*'
gregale jobs run nightly-report --tasks 1 --results --out-dir ./exports
```

`--results` implies waiting and artifact collection. Without `--out-dir`, the destination is `./results/JOB/RUN_ID/`, using the ID returned by the single submission. It never looks up `latest` or submits again while waiting or collecting. Task count or input bindings remain required as for ordinary `jobs run`. Explicit destinations, filters, `--wait-timeout`, and `--poll-interval` remain available. Matching files in a fresh run's destination are treated as conflicts; recovery commands use `--resume` to verify existing files. Failed runs do not start collection. Human output shows the destination; JSON includes `output_directory` and recovery commands, including when waiting times out. Missing directories are created only if collection needs them; preview existing runs with `jobs results JOB --dry-run`.

### Save project-local Job defaults

```bash
gregale jobs defaults set nightly-report --tasks 1 --results
gregale jobs defaults show
gregale jobs run
gregale jobs results
gregale jobs run --tasks 3 --results=false
gregale jobs defaults clear
```

Defaults are stored in `.gregale/jobs.json` at the linked project root, otherwise the Git root, otherwise the current directory. Commands in subdirectories of a linked or Git project use the same defaults. Only the Job name, task count, results and image-wait preferences, and artifact filters are stored; no credentials or environment values. `set` replaces the entire defaults record; omitted task count is unset and omitted `--results` is false. These commands are local and do not require login or modify a remote Job. `clear` is safe to repeat.

`jobs run` fills an omitted Job name, task count, and results preference. Run options apply only to the configured Job, including when its name is explicitly supplied. An explicitly different Job does not inherit task or results defaults. Explicit flags override saved values; input bindings/manifests suppress the saved task count. Use `--results=false` to disable automatic collection. `jobs results` uses the saved Job name when no positionals are provided and saved artifact filters for that Job, and otherwise retains its latest-run and destination defaults. Human output identifies applied defaults and their file. Invalid defaults stop commands before submission; `show` helps inspect them and `clear` removes them. Existing commands work without a defaults file.

### Remember the outputs you normally collect

```bash
gregale jobs defaults set nightly-report --tasks 1 --results --include '*.csv' --exclude 'debug-*'
gregale jobs run
gregale jobs results
gregale jobs results --include '*.json'
gregale jobs results --all-artifacts
gregale jobs run --all-artifacts
```

Saved repeatable `--include`/`--exclude` patterns apply to `jobs results` and to `jobs run` when collection is enabled, only for the configured Job. They also apply when that Job name is explicitly supplied. Any explicit include or exclude replaces **both** saved filter lists, rather than adding to them. `--all-artifacts` ignores saved filters for one invocation; combining it with explicit filters is rejected. It does not enable collection on its own or change saved defaults. Exclusions still take precedence. Effective filters appear in collection/preview JSON and recovery commands, so recovery does not depend on defaults staying unchanged. `defaults show` displays saved filters; `defaults set` replaces the record and omitting filters clears them. Invalid patterns are rejected before saving or making API requests. Existing defaults files without filters remain valid.

### Preview a Job run before submitting

```bash
gregale jobs run --dry-run
gregale jobs run nightly-report --tasks 3 --results --dry-run
gregale jobs run --dry-run --json
```

`jobs run --dry-run` applies local Job defaults and explicit overrides, validates arguments, and shows the effective task selection, run overrides, waiting, results destination, and artifact filters. It requires no login and makes no API requests or file writes. The default destination contains `<run-id>` because no run has been submitted. An explicit destination is inspected without creating it. This preview does not fetch remote Job defaults or readiness, read input manifests, or check account limits. Remote-derived values are marked unknown rather than guessed; input bindings determine the task count when present. Environment values, argument values, and input references are redacted in human and JSON output. JSON contains a sanitized `request_preview`, collection options, and a `dry_run` marker; it is not a submission receipt. Remove `--dry-run` to submit with those options.

### Change one saved Job setting

```bash
gregale jobs defaults update --tasks 3
gregale jobs defaults update --results=false
gregale jobs defaults update --include '*.json'
gregale jobs defaults update --all-artifacts
```

`defaults update` changes an existing defaults record and preserves omitted settings. A positive `--tasks` replaces the saved task count; `--results=false` disables default collection. Supplying either filter flag replaces both saved include/exclude lists. `--all-artifacts` clears both lists and cannot be combined with explicit filters. An optional Job name changes the selected Job while retaining other saved settings. At least one change must be supplied; missing or invalid defaults are reported without creating or replacing a record. Writes use the same atomic replacement and private file permissions as `defaults set`. The command is local, requires no login, and returns the complete updated record and path with `--json`. Use `defaults set` to replace everything or `defaults clear` to remove the record.

### Check the latest Job status with fewer arguments

```bash
gregale jobs status
gregale jobs status nightly-report
gregale jobs status --check --json
gregale jobs status nightly-report RUN_ID --check
```

Without arguments, `jobs status` reads the saved Job name from project-local defaults. With a Job name but no run reference, it selects `latest`. Latest selection happens once; the summary and follow-up commands refer to that exact UUID. Explicit Job/run targets do not read local defaults. Missing or invalid defaults are reported before API requests when no Job is supplied. Status remains read-only and preserves the existing run JSON shape. `--check` exits successfully only for a recorded `succeeded` run, without waiting or submitting anything.

### Inspect tasks without repeating the Job and run

```bash
gregale jobs tasks
gregale jobs tasks --failed-only
gregale jobs tasks --artifacts-only
gregale jobs tasks nightly-report --all
gregale jobs tasks nightly-report RUN_ID --failed-only
```

Without positionals, `jobs tasks` uses the saved Job name. An omitted run reference defaults to `latest`, resolved once; pagination, log, and artifact follow-up commands use that exact UUID. Explicit Job/run targets do not read local defaults. Existing pagination, failed-task filtering, artifact-name filters, JSON task records, and interactive behavior remain available. Task inspection uses only the saved Job name, not the saved task count, results preference, or collection filters; pass artifact filters explicitly with `--artifacts-only`. Missing or invalid defaults are reported before API requests when no Job is supplied. This command remains read-only and does not download artifacts or create files.

### Read task logs with a shorter command

```bash
gregale jobs logs --task 0
gregale jobs logs nightly-report --task 0
gregale jobs logs nightly-report RUN_ID --task 0 --max-bytes 4096
gregale jobs logs --task 0 --attempt 2
```

With `--task`, the Job name defaults to the saved project-local Job and the run reference defaults to `latest`, resolved once. The task index remains explicit and must be nonnegative. `--attempt` and `--max-bytes` retain their current behavior. The original `jobs logs JOB RUN_ID|latest TASK_INDEX` syntax and JSON log response remain available. Explicit Job names bypass local defaults. Duplicate task flags or mixing `--task` with the positional task index are rejected before API requests. This is read-only; it does not guess a task, retry, download artifacts, or create files. Human output identifies the defaults file when it supplies the Job name.

### Inspect saved Job history

```bash
gregale jobs runs
gregale jobs runs --failed-only
gregale jobs runs --failed-only --all
gregale jobs runs nightly-report --status succeeded
```

Omitting the Job name uses the project-local saved Job; an explicit name bypasses local defaults. Existing limits, offsets, all-page traversal, and JSON run records retain their behavior. `--failed-only` filters fetched history to `failed` and `dead_letter` runs and cannot be combined with `--status`; cancelled runs are excluded. Filters apply to the fetched pages, so use `--all` to scan subsequent pages. Human follow-up commands retain the selected Job and history filter. This command is read-only and does not resolve `latest`, inspect task logs, submit runs, or collect outputs. Missing or invalid defaults stop an omitted-name command before API requests.

### Inspect the saved Job and check image readiness

```bash
gregale jobs info
gregale jobs info --check
gregale jobs info --wait-ready
gregale jobs info nightly-report --wait-ready --timeout 5m
```

`jobs info` and its readiness options use the saved Job when no name is supplied; explicit names bypass local defaults. `--check` reads once and exits 0 only when the recorded Job state is `active` and its image preparation status is `ready`. Paused Jobs, pending or failed images, and unknown states fail the check. It preserves the normal Job JSON response on stdout and reports failures on stderr. This check does not assess account limits, input accessibility, or guarantee execution success. `--check` and `--wait-ready` are mutually exclusive. Image waiting retains its existing behavior: it waits for the selected image, regardless of the Job's active/paused state, and pins Job/image identity. Wait timeout and polling flags require `--wait-ready`. No option submits a run or changes the Job.

### Wait for image preparation before starting a Job

```bash
gregale jobs run --wait-ready --results
gregale jobs run nightly-report --tasks 1 --wait-ready --image-wait-timeout 5m
gregale jobs run --wait-ready --dry-run
```

`--wait-ready` reads the selected Job and waits for its image to become ready before making the single run submission. Job/account/image identity and any resolved digest are pinned during polling. Image failure, unknown preparation state, changed identity, read failure, cancellation, or deadline stops before submission. `--image-wait-timeout` (default 5m) and `--image-poll-interval` (default 2s) must be positive and require `--wait-ready`. The image deadline includes the initial read and is separate from post-acceptance waiting and artifact collection.

Defaults, explicit overrides, filters, and `--results` continue to apply. `--dry-run` previews the image-wait settings locally without reading the Job or submitting. Pre-submission JSON failures have `phase: "image_wait"`, `submitted: false`, an exit code, and an inspection command; successful submissions retain existing receipts. Timeout exits 124; cancellation exits 130. Image readiness alone does not check active state, account limits, or input accessibility. The server validates acceptance; the submission API does not atomically pin the image against a concurrent update after polling. Uncertain submission errors still direct users to history before resubmitting.

### Remember image waiting for a Job

```bash
gregale jobs defaults update --wait-ready
gregale jobs run
gregale jobs run --wait-ready=false
gregale jobs defaults update --wait-ready=false
gregale jobs defaults set nightly-report --tasks 1 --results --wait-ready
```

Saved `--wait-ready` applies to runs of the configured Job when that flag is omitted. Explicit `--wait-ready=false` disables it for one run; other Jobs do not inherit it. `defaults show` and `run --dry-run` display the preference. `defaults update` preserves it when omitted, and `defaults set` resets it to false when omitted. Existing files without `wait_ready` remain valid and default to false. Image timing options can be supplied when saved image waiting is enabled; they are still per-invocation options and must be positive. Disabling waiting while supplying image timing flags is rejected. Status, task, log, info, and results commands do not inherit this run preference. The existing image preparation timeout, cancellation, and single-submission behavior remain unchanged.

### See Job readiness and the latest run together

```bash
gregale jobs overview
gregale jobs overview nightly-report
gregale jobs overview --json
```

`jobs overview` uses the saved Job when its name is omitted and otherwise bypasses local defaults. It shows Job state, image preparation, and the latest run's status and task counts. The latest history selection is read once; details and follow-up commands use that exact UUID. Jobs with no recorded runs still show readiness and configuration guidance. If the latest run has failures or is failed/dead-letter, overview scans failed task metadata and offers log commands for up to three tasks, plus a command to inspect all failed tasks. It does not read log content or artifact manifests/download URLs. Succeeded runs offer results collection; queued/running runs offer waiting. Non-ready images offer image-wait guidance.

Overview makes only read requests, has a 30-second overall deadline, and never starts runs, waits for completion, downloads outputs, or writes files. Read/identity failures return an error instead of presenting an incomplete summary or falling back to an older run. The JSON summary contains only selected Job/run status fields and action commands, excluding environment snapshots and storage references. Counts are recorded snapshots and may change while the command runs. Generated commands retain the current profile; results commands continue to use matching local collection defaults when executed.

### Understand the overview at a glance

`jobs overview` now adds separate readiness and latest-run assessments in human output and JSON (`readiness_assessment`, `run_assessment`). It describes paused/active Job state independently from ready/preparing/failed image state, so a succeeded previous run does not hide current preparation problems. Run assessments distinguish queued, in-progress, succeeded, failed, dead-letter, cancelled, and unknown states; no history reports “No runs recorded yet.” Active-run progress counts succeeded, failed, and cancelled tasks as finished, excludes overlapping dead-letter counts, and omits the fraction when recorded counts are inconsistent or the total is unknown. These are recorded snapshots, not execution guarantees. Assessments use already-fetched metadata and add no API requests. Failed or unknown image states offer inspection instead of waiting for a known failure.

### Use the overview as a health check

```bash
gregale jobs overview --check
gregale jobs overview nightly-report --check --json
```

`--check` prints the normal overview and exits 0 only when the recorded Job state is active, image preparation is ready, and the latest run succeeded. Paused/unknown Job states, non-ready images, other run outcomes, and no recorded runs fail. All failed conditions are reported together on stderr. JSON adds `check: {passed, reasons}` to the overview only when the flag is enabled; the reasons array is empty when it passes. Human output marks a successful check. Without `--check`, overview retains its existing output and exit behavior. The check uses already-fetched metadata, adds no requests or mutations, and assesses recorded state rather than guaranteeing a future run will succeed. Read/identity errors remain errors and do not produce a partial health check.

### Correct common run argument errors

Run validation errors include correction guidance for missing saved Jobs or task selection, filters without collection, empty destinations, timing options without waiting, conflicting artifact filters, input-manifest combinations, malformed input bindings, and negative retry counts. Run examples use `--dry-run` to let you inspect corrected settings before submitting. Examples are illustrative: choose the task count and input values your Job needs, and retain your other options. Guidance never reprints supplied environment values, argument values, or input references. With `--json`, corrections remain in the stderr problem detail and stdout stays empty for validation failures. Validation still stops before API requests or file writes. Inspect `jobs defaults show` before updating a saved task count, especially when running a different explicit Job.

### Wait for the saved Job's latest run

```bash
gregale jobs wait
gregale jobs wait nightly-report
gregale jobs wait --timeout 5m
gregale jobs wait --out-dir ./results --include '*.csv'
```

Without positionals, `jobs wait` uses the saved Job name. An omitted run reference defaults to `latest`, resolved once, then all polling and recovery commands use that exact UUID. Explicit Job names bypass local defaults. Existing timeout/polling flags, exit codes, JSON receipts, explicit run IDs, and `--out-dir` collection with explicit filters retain their behavior. Only the saved Job name is used: run task counts, image waiting, results preferences, and saved collection filters do not apply to `jobs wait`. Waiting never submits a run. Missing or invalid defaults stop omitted-name commands before API requests. Existing interactive behavior is unchanged; interactive collection remains unsupported.

### Recover from a failed readiness check

Failed `jobs info --check` and `jobs overview --check` now include suggested next commands on stderr. Preparing images offer `jobs info JOB --wait-ready`; failed/unknown images and paused/unknown Job states offer inspection. Overview additionally offers exact-run waiting for active runs, failed-task inspection and available task log commands for failed/dead-letter runs, task inspection for cancelled/unknown outcomes, or a local first-run preview when there is no history. Duplicate commands are omitted. JSON stderr problem details add `suggested_commands`, an array of `{purpose, command}`; existing stdout Job/overview receipts and check exit codes remain intact. Commands preserve the current profile and exact run UUID. Suggestions use already-fetched metadata and add no requests. Nothing is run automatically; first-run guidance uses `--dry-run` and never submits a Job.

### Read all failed-task logs in one command

```bash
gregale jobs logs --failed-only
gregale jobs logs nightly-report --failed-only --max-bytes 4096
gregale jobs logs nightly-report RUN_ID --failed-only
```

`--failed-only` uses the saved Job if omitted and resolves an omitted/latest run once. It validates Job/run identity, scans all task pages, and reads current logs for tasks recorded as failed, timeout, or oom; cancelled and succeeded tasks are excluded. Each log tail is bounded by `--max-bytes` (default 65536, maximum 1048576), with excess server output also truncated locally at a UTF-8 boundary. Human output labels Job, exact run, task index, discovered attempt/status, and current log status. JSON streams one record per task with those identifiers, followed by a `type: "summary"` record; no matching tasks produces only a zero-count summary and exit 0. Tasks can change between discovery and log reads; this reads current logs, not a pinned retained attempt.

`--failed-only` cannot be combined with a positional task index, `--task`, or `--attempt`; use a single-task command for retained output. It has a five-minute overall deadline and 30-second read deadlines, handles cancellation, and never submits, retries, or writes files. An individual read failure is recorded in the summary and collection continues; the command exits nonzero after reading remaining tasks. Explicit Job names bypass local defaults; saved collection filters do not apply. Legacy single-task log syntax and JSON remain unchanged.

### Scan the failed-log summary

`jobs logs --failed-only` ends with counts of failed tasks found, log reads completed, empty tails, truncated tails, unavailable logs, and remaining tasks not attempted. Empty and truncated counts describe successful reads and may overlap. Reading continues past individual unavailable logs; previously printed logs remain available and the final exit code stays nonzero. Each unavailable task has an exact log-read retry command preserving Job, run UUID, profile, and byte limit. No retry is run automatically.

JSON stdout retains existing task records and appends a final record with `type: "summary"`, `job`, `run_id`, `found`, `read`, `empty`, `truncated`, `not_attempted`, and `unavailable` (task indexes and retry commands). Consumers wanting only task logs should filter out summary records. Once task discovery succeeds, the summary is emitted even if a later log read fails; the stderr problem remains separate. Discovery failures do not claim a complete task count. A broken output stream may prevent the summary from being written. The summary uses existing responses and makes no additional requests.

### Collect the remaining logs after an individual failure

Failed-task log collection continues to later tasks after individual API, transport, or per-read timeout errors, without retrying automatically. The final summary lists every unavailable task, a redacted error, and an exact log-read retry command. Successfully read task records remain streamed in discovery order. A partial collection exits nonzero using the first individual error's classification. Cancellation stops further reads and exits 130; the five-minute overall deadline stops further reads and exits 124. Those stop reasons take precedence over earlier individual failures, and the summary distinguishes already-attempted unavailable logs from remaining tasks not attempted. Discovery and identity failures still stop before log reads. In JSON, unavailable entries add `error`; the summary remains the final stdout record and the terminal problem is on stderr.

### Go directly from a failed run to its logs

Failed/dead-letter run guidance from `jobs status`, `jobs wait`, `jobs runs` (including failure/status filters), and `jobs overview` now includes `jobs logs JOB RUN_ID --failed-only` alongside task inspection. Overview check recovery suggestions also include it. The current profile and exact run UUID are preserved; cancelled-only runs do not suggest failed-task logs. Guidance uses already-fetched metadata and makes no extra API requests. Status, waiting, and history keep their existing JSON shapes; overview adds the command to its action list and failed-check suggestions. No log command runs automatically.

### Collect results directly from successful-run guidance

Successful run guidance from `jobs status`, `jobs wait`, and `jobs runs` (including `--status succeeded`) now offers `jobs results JOB RUN_ID` alongside artifact discovery. Commands preserve the current profile and exact returned run UUID. History guidance remains capped at three unique succeeded runs and indicates how to inspect the remaining runs. No extra requests are made and no outputs are downloaded automatically. Executing the suggested results command uses its existing run-specific destination, matching local collection filters, and verification of existing files. Success does not guarantee recorded artifacts; Jobs with no managed outputs create no result folders. Existing status, waiting, and history JSON shapes remain unchanged.

### Retry a task without copying the run ID

With a Job saved using `jobs defaults set`, retry a task in its newest run:

```sh
gregale jobs retry --task 2
gregale jobs retry --task 2 --wait
```

Override the saved Job with `jobs retry nightly-report --task 2`, or pin a run
with `jobs retry nightly-report RUN_ID --task 2`. An omitted run selects the
first run in Job history, including pending or successful runs. The CLI resolves
that selection once, verifies Job/run ownership, and sends one retry request to
that exact UUID. The server enforces task state and retry budget; the CLI does
not search older runs or automatically repeat a retry request. Inspect with
`jobs tasks --failed-only` first when choosing a task. `--wait` assesses the
whole run, including other tasks. Existing `jobs retry JOB RUN_ID TASK_INDEX`
syntax remains available. Saved task counts, result settings, and filters do
not apply to retry.

### Collect results after a task retry

```sh
gregale jobs retry --task 2 --results
gregale jobs retry nightly-report RUN_ID 2 --results --include '*.csv'
```

`--results` implies waiting for the whole run to succeed, then collects current
managed outputs into `./results/JOB/RUN_ID/`, with one subdirectory per task.
It uses the same selected UUID throughout and submits one retry request.
Matching saved artifact filters apply; explicit `--include` or `--exclude`
replaces that selection, while `--all-artifacts` ignores saved filters.
These filter options require `--results`. Saved task counts and the saved
run `results` preference do not automatically enable retry collection.

Existing matching files are verified and skipped. Conflicting files are not
overwritten. Failed runs, cancellation, and wait timeouts do not start
collection. Use `--wait-timeout` and `--poll-interval` to control waiting.
If waiting or collection fails after acceptance, follow the exact-run recovery
command in the output; it continues without submitting another retry.

### Replay unsuccessful tasks without copying the source run ID

```sh
gregale jobs replay-failed
gregale jobs replay-failed --wait
gregale jobs replay-failed nightly-report latest --wait
```

An omitted Job uses the project-local Job default. An omitted run, or `latest`,
selects the first run in that Job's history, including pending or successful
runs. The CLI resolves it once and checks Job/run ownership before sending one
replay request for the selected UUID. The server checks replay eligibility;
the CLI does not search older runs or automatically repeat the request.
Replay creates a new linked recovery run. `--wait` follows that new run rather
than the source. `--wait-timeout` and `--poll-interval` control waiting.

Pin a source explicitly with `jobs replay-failed JOB RUN_ID`; explicit Job
names bypass local defaults. Saved task counts, collection preferences, and
artifact filters do not apply to replay. If submission has an uncertain
outcome, inspect run history before submitting again.

### Collect outputs from a replay recovery run

```sh
gregale jobs replay-failed --results
gregale jobs replay-failed nightly-report RUN_ID --results --include '*.csv'
```

`--results` implies waiting for the new linked recovery run to succeed, then
collects its current managed outputs into `./results/JOB/NEW_RUN_ID/`, with a
subdirectory per task. It collects from the recovery UUID returned by the one
replay request, rather than the source run or a later `latest` selection.
Matching saved artifact filters apply. Explicit `--include` or `--exclude`
replaces them; `--all-artifacts` ignores them. Filter options require
`--results`. Saved task counts and the run collection preference do not
automatically enable replay collection.

Matching existing files are verified and skipped; conflicts are not
overwritten. A failed recovery run, cancellation, or wait timeout does not
start collection. `--wait-timeout` and `--poll-interval` control waiting.
After acceptance, recovery commands continue waiting or collecting from the
same recovery UUID without submitting another replay. An uncertain submission
or an invalid acceptance response requires inspecting run history first.

### Inspect earlier task attempts without copying run IDs

```sh
gregale jobs attempts --task 2 --failed-only --all
gregale jobs attempts nightly-report --task 2
gregale jobs attempts nightly-report RUN_ID --task 2 --limit 20
```

With `--task N`, an omitted Job uses the project-local Job default, and an
omitted run uses `latest`. The newest run is resolved once; all attempt pages
and retained-output guidance use that same UUID. Explicit Job names bypass
local defaults. Saved task counts, result preferences, and artifact filters do
not apply to attempt inspection. A task index is required and must be
nonnegative; do not combine `--task` with a positional task index.

Existing `jobs attempts JOB RUN_ID|latest TASK_INDEX` syntax, JSON output,
`--failed-only`, `--all`, `--limit`, and `--offset` remain available. Attempt
history contains retained terminal outcomes; active or expired attempts may
not appear. This command reads metadata without fetching logs, downloading
artifacts, or retrying tasks.

### List task outputs without copying run IDs

```sh
gregale jobs artifacts --task 2
gregale jobs artifacts --task 2 --attempt 1
gregale jobs artifacts nightly-report RUN_ID --task 2 --include '*.csv'
```

With `--task N`, an omitted Job uses the project-local Job default, and an
omitted run uses `latest`. The CLI resolves the newest run once; metadata reads
and current-output download guidance use that same UUID. Explicit Job names
bypass local defaults. Saved artifact filters do not apply to this inspection;
use explicit `--include` or `--exclude` to filter names. Task indexes must be
nonnegative. Do not combine `--task` with a positional task index.

The existing `jobs artifacts JOB RUN_ID|latest TASK_INDEX` syntax and JSON
output remain available. `--attempt N` inspects retained metadata only; it
does not fall back to current outputs when an attempt is missing. Listing
reads metadata without downloading files or requesting signed download URLs.

### Download a named task output without copying run IDs

```sh
gregale jobs artifact-download --task 2 report.csv --out report.csv
gregale jobs artifact-download nightly-report --task 2 report.csv --out report.csv
gregale jobs artifact-download nightly-report RUN_ID --task 2 report.csv --out report.csv
```

For a single artifact, `--task N` allows an omitted Job to use the project-local
Job default and an omitted run to use `latest`. The newest run is resolved
once and that UUID is used for the artifact request and receipt. Explicit Job
names bypass local defaults. Saved task counts, result preferences, and
artifact filters do not apply. Use `--out NEW_FILE` with an existing parent
directory; existing files and symlinks are not overwritten. Download still
verifies the artifact's name, size, and SHA-256.

`--task` requires a nonnegative index and cannot be combined with a positional
task index, `--all`, or `--all-tasks`. Existing single-artifact and bulk
positional syntax remains available. For an artifact name starting with a
hyphen, place flags first and the artifact after `--`:

```sh
gregale jobs artifact-download --task 2 --out ./saved.csv -- -report.csv
```

## Check required secret configuration

Use `gregale secrets check --app my-api --required DATABASE_URL --required API_KEY`
to compare explicitly required keys with sealed-secret metadata in one scope.
The app and scope default to the linked project context. Select another scope
with `--scope staging`; `__all__` is rejected to avoid mixing environments.

For a reusable local declaration, create `.gregale/required-secrets.json`
containing only key names:

```json
["DATABASE_URL", "API_KEY"]
```

Run `gregale secrets check --required-file .gregale/required-secrets.json`.
The file is bounded to 64 KiB and contains no values. Without explicit
requirement selectors, it is discovered automatically at the project root.
Repeated `--required` keys are combined with the file and deduplicated.
Without requirements, completeness remains unknown.

The summary distinguishes missing keys, current-version runtime-start delivery,
and application acknowledgement. Acknowledgement is confirmed only for a
complete nonempty authorized runtime roster at the current nonzero version.
Missing keys, pending/failed/unconfirmed delivery, and current application
acknowledgement failures exit 1; command/read errors also exit nonzero.
Unconfirmed application adoption is shown explicitly even when delivery succeeds.
The check does not validate ordinary environment variables, reference destinations,
or application behavior. It performs no remote writes, restarts, or secret-value reads. The optional
`--missing-template` export writes a private local file.
`--json` returns a selected key/status projection without values, hashes, sealing
identities, or runtime identifiers.

## Read logs for the latest failed deployment

Run `gregale logs --latest-failure` in a linked project, or
`gregale logs my-api --latest-failure` with an explicit app. This read-only,
noninteractive shortcut selects the newest failed deployment from app-scoped
history, including failures without a typed error code. It resolves the target
once and reads at most 500 recent retained build/deploy entries for that exact ID.
A newer successful deployment does not replace the selected failure.

Selection uses app deployment history without an environment filter; the receipt
states that scope, the connection profile, exact deployment, source, and limit.
Deployment creation time is labeled separately from the unknown failure time.
The endpoint has no time-window selector. This shortcut does not inspect runtime
crashes or HTTP request failures, and does not automatically query runtime logs.
It accepts only an app target alongside `--latest-failure`; other filters, saved
views, follow, and archive modes are rejected before API access.

An empty log result points to `deployment get ID` for persisted evidence. It does
not claim that retention expired or that nothing executed. No failed deployment
exits 1; unreadable or incomplete history fails without switching to live logs.
History search is bounded to 50 pages with repeated-cursor protection, and the
whole read has a 30-second deadline (exit 124) with interrupt support (exit 130).

With `--json`, stdout is newline-delimited JSON: a selected metadata receipt,
`log` records containing the exact deployment ID and line, and a completion
summary. API/stream failures exit nonzero without a success summary.

## Understand which linked targets commands use

`gregale context` shows the linked project, default app and environment,
connection profile and API sources, absolute project-context path, and absolute
connection-configuration path. It explains missing defaults and how selectors
work: the nearest ancestor `.gregale/project.json` wins, explicit selectors
supersede linked defaults, and the environment applies only to commands that
support environment selection. A profile selects a connection and credentials;
it is not an environment. When `FAAS_API` overrides the profile API address,
the display explains that credentials are still selected by the profile.

Use `gregale context --check` for a local guard before app-scoped commands. It
exits 1 if no project is linked or no default app is recorded, and otherwise 0.
An unset environment is explained but does not fail: scoped secrets use
`default`, and other commands keep their own defaults. Malformed context or
connection configuration fails instead of displaying a silently inferred target.
The check makes no API requests or writes and does not validate remote resources,
project membership, credentials, or health.

JSON output keeps the existing context/path/connection fields and adds a
`targeting` projection with status, configuration paths, app/environment sources,
notes, and a suggested discovery command for missing links or app defaults.

## Resume watching a locally recorded accepted run

Successful `jobs run` and `workflows run` submissions now save an atomic private
identifier record below `.gregale/accepted-runs/` at the nearest linked project
root, otherwise Git root, otherwise current directory. Job replay saves the new
recovery run ID; accepted Job retries and Workflow retries/resumes also record
the returned run when its identity matches the request. Unknown/failed
submissions are never inferred from history or automatically repeated.

Use `gregale jobs resume` or `gregale workflows resume-wait` to continue read-only
observation of a single recorded run. These commands do not submit, retry, resume
execution, cancel, or collect outputs. Workflow `resume-wait` is distinct from
`workflows resume`, which changes execution. Completed runs remain recorded;
they are not automatically pruned or assumed to be the latest run.

When several records exist, inspect `--list`, then select `--run UUID` explicitly.
The selected record must match the current connection profile and API address;
otherwise observation stops before network access. Select the original profile
with the global `--profile NAME` option and restore any required API override.
Neither credentials nor connection settings are automatically switched.
`--timeout` defaults to 10m and `--poll-interval` to 2s. Existing exact-run wait
commands remain available for older runs that have no local record.

Records contain identifiers and connection addresses only: no credentials,
idempotency keys, input payloads, environment values, or result payloads.
Files are created with mode 0600, bounded to 8 KiB, and each identity has its own
file so concurrent distinct submissions cannot lose each other's records.
Symlinked record directories/files are rejected. Malformed or excessive local
records stop selection rather than silently picking another run.
A storage failure prints a warning after acceptance and preserves the original
submission outcome; keep the returned run ID instead of submitting again.
`--list --json` returns selected identifier records with no API access; waiting
uses the existing Job or Workflow receipt and exit-code behavior.

## Review and forget saved run records

Run `gregale jobs resume --overview` or
`gregale workflows resume-wait --overview` to group locally recorded runs into
in-progress, finished, and unavailable results. Each row includes its exact run
ID and a POSIX command to observe that run with its original API/profile.
Finished includes failed or cancelled outcomes; it does not mean success.

The overview reads only runs recorded for the current API and profile. Other
connections remain visible as unavailable without requests or credential
switching. Missing runs, unreadable status, unknown states, and identity
mismatches are reported without guessing outcomes or deleting records. One read
is attempted per matching record, with a 5-second per-read limit and 30-second
overall deadline by default (`--timeout` overrides the overall deadline).
Unavailable rows exit 1; deadline and interrupt return 124 and 130 respectively,
with partial observations preserved. JSON groups selected identity/status rows
without input or result payloads.

Use `gregale jobs resume --forget UUID` or
`gregale workflows resume-wait --forget UUID` to remove one selected local record
under the current recorded API/profile. No login or API request is needed.
Remote execution, results, and history remain unchanged, including for active
runs. The receipt identifies the forgotten record; JSON includes
`remote_changed: false`. No completed records are automatically removed.

`--overview`, `--list`, and `--forget` are separate modes. Use `--run UUID` only
for resuming observation. Timing options are rejected for list/forget, and
`--poll-interval` is rejected for the one-pass overview. Invalid, ambiguous,
changed, or symlinked records fail without removing another record.


## Discover declared secret requirements automatically

With no requirement flags, `gregale secrets check` discovers
`.gregale/required-secrets.json` at the nearest linked project root, otherwise
Git root, otherwise current directory. A linked root takes precedence over a
nested directory's declaration, matching where project defaults are stored.
The linked app and environment remain the command's normal defaults; explicit
`--app` and `--scope` still select the target being checked.

The JSON array contains only declared key names, for example
`["DATABASE_URL", "API_KEY"]`. Human and JSON output identify the absolute
selected declaration path and requirement source. An empty array is an explicit
empty declaration; a missing file leaves completeness unknown. Invalid,
oversized, unreadable, or symlinked discovered declarations fail before login
or API access instead of silently ignoring requirements. Nothing is created or
written during discovery, and source files or environment examples are not
scanned to infer required keys.

Any explicit `--required` keys replace automatic discovery. An explicit
`--required-file PATH` also replaces discovery; if combined with `--required`,
its keys and the explicit keys are combined as before. Empty explicit paths are
rejected. The JSON source is `project_file`, `flags`, `explicit_file`,
`explicit_file_and_keys`, or `none`.

The `.gregale/` directory is normally ignored by Git. To share a tracked
requirement declaration elsewhere, use `--required-file config/required-secrets.json`.

## Export missing required names into a private template

Run `gregale secrets check --missing-template missing.env` to create a local
dotenv template containing only missing explicitly required names:

```dotenv
# Fill every value before uploading. Empty assignments are placeholders.
API_KEY=
DATABASE_URL=
```

Requirements can come from project discovery, `--required`, or `--required-file`.
The destination must be a new file in an existing real directory. Existing
files, directories, symlinks, and symlinked parent components are rejected
before login or API access. Export refuses undeclared requirements rather than
inventing names. It writes mode 0600 through a temporary file and publishes
without overwrite; temporary files are removed on failure.

Existing configured keys and all existing values are excluded. If no required
keys are missing, no file is created. API-read failures create no output.
The template receipt lists the absolute path, missing names, and creation status.
JSON includes this receipt under `missing_template`. A successful export keeps
the check's nonzero exit when required secrets are missing.

After filling every value locally, use the displayed `env push --dry-run`
command to preview the upload for the exact app, scope, and profile. The export
never uploads, restarts, or changes remote configuration. Files filled with
secrets should stay private and outside version control.

## Check required keys before uploading a filled template

`gregale env push -f missing.env --dry-run` now reads the same project-root
`.gregale/required-secrets.json` declaration used by `secrets check`. The preview
compares required names against current scoped secret metadata and the final
proposed values after scan exclusions. Duplicate entries follow upload order:
the last kept value for a key determines whether its proposed value is empty.

Missing required keys and empty proposed required values are listed by name,
make `can_push` false, and exit 1. Optional empty values keep existing behavior.
A required key already configured in the selected scope need not appear in the
input file, but its existing value remains unverified because plaintext is not
read. Metadata from another scope does not satisfy a required key. Scanned-out
or oversized pairs do not establish proposed nonempty coverage.

The human summary and JSON `requirements` projection show the declaration path,
coverage, missing names, empty proposed names, and existing values that remain
unverified. `keys_covered` describes names and proposed nonempty values after a
successful upload; it does not verify existing values, delivery, application
behavior, or override other preview blockers. No declaration leaves coverage
unknown and adds no requirement blockers. An explicitly empty declaration is
recognized; malformed, oversized, unreadable, or symlinked declarations fail
before login or API reads.

These requirement checks apply to `--dry-run`. The preview uploads no values,
requests no restart, and writes no local files. Actual uploads keep their
existing server validation and execution behavior.

## Read failure evidence for a saved run

`gregale jobs resume --logs` selects an exact local accepted-run record and reads
current failed-task logs using the existing bounded failed-log collector. It
includes failed, timed-out, and out-of-memory tasks; it does not tail every task
or select a historical attempt. No failed tasks produces the existing empty
summary. Individual unavailable logs remain visible in the collection receipt.

`gregale workflows resume-wait --diagnose` reads diagnostics for the exact saved
Workflow run. It checks the recorded run, workflow, and available app identity
before reading diagnostics, and verifies the diagnostic response identifies the
same run and workflow. Neither shortcut waits, retries, resumes execution, or
submits work.

With several local records, pass `--run UUID` explicitly. Both shortcuts require
the recorded API/profile and stop before network access on connection mismatch
or ambiguous selection. Evidence modes accept only `--run`; list, overview,
forget, and timing flags must be used separately. Jobs accept `--logs`, while
Workflows accept `--diagnose`. JSON uses the existing failed-log stream/summary
or diagnostic response. Records are retained and credentials are not switched.

## Collect outputs from an exact saved Job run

`gregale jobs resume --results` selects the exact recorded run, verifies its
Job/account identity and original API/profile, waits for success, and collects
artifacts through the existing `jobs results` path. When multiple records exist,
pass `--run UUID`; no history lookup chooses a newer run. Workflow resume does
not accept `--results`.

Output defaults to `./results/JOB/RUN_ID` relative to the current directory.
The results receipt shows the absolute destination and exact run. Existing
matching files are verified and skipped; mismatched files are not overwritten.
Saved artifact filters apply under the existing matching-Job rules. For custom
destinations, filters, or a read-only collection preview, use `jobs results JOB
RUN_ID` with its existing options.

`--timeout` and `--poll-interval` control waiting as in normal resume. Failed,
cancelled, interrupted, and timed-out waits do not start collection; existing
exact-run recovery and failed-task log guidance remain available. Records are
retained. The shortcut does not submit or retry remote work. `--results` cannot
be combined with logs, list, overview, or forget modes.

## Follow the next action from a saved-run overview

The saved-run `--overview` now suggests a command matched to each verified
state. Running or queued work keeps its exact observation command. Successful
Jobs suggest `--results`; failed or dead-letter Jobs suggest `--logs`. Failed,
dead, or awaiting-event Workflows suggest `--diagnose` so users can inspect the
failure or event wait. Awaiting an event is not automatically a failure.

Cancelled runs and unavailable or unrecognized observations keep the existing
observation command without guessing that failed logs or successful artifacts
exist. Every command preserves the recorded API, profile, and exact UUID.
Suggestions are printed only: the overview never downloads results, reads extra
logs/diagnostics, or executes recovery actions. Result collection happens only
when the displayed collection command is explicitly run.

JSON retains `resume_command` and adds `next_action` (`observe`, `results`,
`logs`, or `diagnose`) and `next_command` to each row. Existing grouping and
exit-code behavior remain unchanged.

## Change one linked default without relinking

Use `gregale context set --environment staging` or
`gregale context set --app payments-api` to change only that local default.
Both may be supplied together. Omitted fields and the linked project remain
unchanged; no workload or environment is automatically inferred. The nearest
existing project context is updated, including when invoked from subdirectories.
This command requires a valid existing link; use `link` to change projects.

Setting an app checks that it appears in the linked project's workload list.
Setting an environment checks its slug and project identity. Validation uses the
current connection with a 30-second deadline, and failures save no changes.
Before writing, the command re-reads the link and refuses a changed context.
Updates use the existing atomic private context writer. A no-op preserves the
file unchanged. JSON identifies the absolute path, before/after settings,
changed flag, and connection; human output shows both old and new defaults.

`--clear-app` and `--clear-environment` explicitly remove selected defaults.
Clear-only changes are local and require no login or API request. Clearing an
app means app-scoped commands need an explicit target; clearing an environment
restores each command's own default. Setting and clearing the same field,
empty slug values, positional arguments, and updates without an operation are
rejected. Context files and their directory components must be real files and
directories. These changes do not alter remote services, connection profiles,
credentials, project membership, or `.gitignore`.

## Find the latest recorded HTTP server error

`gregale logs --latest-request-error` searches the linked app's retained HTTP
request telemetry for the newest recorded 500–599 response. The default window
is 15m; use `--since 1h` (or an RFC3339 start) and optionally `--route /checkout`.
An explicit app is accepted positionally or through `--app`. Other log filters,
views, follow, archive, and deployment-failure selection are rejected before
API access. The HTTP source is implicit.

Selection inspects up to 50 pages of 200 rows under a 30-second deadline, pins
the server's window across cursor pages, and then looks up the exact selected
public request ID or telemetry row ID. It verifies that the lookup matches the
selected identity, route, status, and timestamp. It never substitutes an older
row when identifiers, coverage, or matching evidence are unavailable.
Incomplete coverage, repeated cursors, invalid error timestamps, changed
windows, and the page cap fail rather than claiming a newest result.

Human output states the requested/effective window, retention clamp, route,
status, IDs, timestamp, and represented-request count. No matching retained
5xx row exits 1 and explains that absence does not establish every request was
captured or succeeded. Aggregated telemetry rows may represent multiple requests;
this shortcut reads HTTP request telemetry, not raw runtime logs or request
payloads. JSON emits one selection receipt followed by the existing canonical
HTTP log record. No matching row emits only the selection receipt. Reads make
no writes, deployments, retries, or recovery actions.

## Verify the account behind your linked targets

`gregale context --verify` performs read-only account, project, and optional
environment reads on the current connection. It reuses profile authentication
checks, shows the authenticated account ID/email/plan, verifies the linked
project identity, checks the default app is a project workload, and verifies
the selected environment belongs to that project. The ordinary context display
and local `--check` keep their existing network-free behavior.

An unset environment is reported as command-specific defaults, without inventing
or verifying a remote default environment. A missing link or app, inaccessible
project/environment, mismatched identity, or authentication failure exits
nonzero and shows one discovery/check command for correction. Unreadable targets
are reported as unverified, not assumed to be deleted or owned by another user.
No connection settings, credentials, links, or remote services are changed.

The whole verification has a 10-second deadline; use `--timeout 5s` with
`--verify` to change it. Timeout/interrupt exit 124/130. JSON preserves existing
context fields and adds a selected `verification` result with account identity,
per-target status, message, suggested command, and exit code. Tokens and raw
account/target payloads are excluded. Endpoint addresses containing credentials,
query parameters, or fragments are rejected before being displayed or contacted.
Verification does not check runtime health or configuration delivery.

## Compare required names across selected environments

`gregale secrets check --environments staging,production` compares the same
explicitly declared required names against sealed-secret metadata in each named
scope. Requirements come from project discovery or existing `--required` and
`--required-file` selectors. The app defaults to the linked app; an explicit
`--app` overrides it. Unlike `env diff`, this comparison reads no ordinary
environment values and compares no secret hashes.

The matrix contains only required names and `configured`, `missing`, or
`unverified` cells. Configured means the key appears in that scope's metadata;
it does not verify its value, delivery, application acknowledgement, reference
destinations, or whether a registered project environment exists. Unreadable
scopes are unverified rather than assumed missing. Each scope needing attention
gets an exact single-scope check command preserving the app, connection profile,
and requirement selectors. JSON includes selected scopes, statuses, names,
declaration source, and those commands; values and hashes are excluded.

Comma-separated scope names are trimmed and deduplicated in first-seen order,
with a limit of 10 distinct scopes. Empty names and `__all__` are rejected.
Comparison requires a declared requirement set; an explicitly empty array is
valid. `--scope` and `--missing-template` cannot be combined with comparison.
The overall read deadline is 30 seconds, with 5 seconds per scope. Other scopes
continue after individual read failures; interrupted/deadline-limited reads
preserve partial observations and mark unread scopes unverified. Missing or
unverified cells exit 1; overall timeout/interrupt exit 124/130. No files,
configuration, or remote services are changed.

## Save the scopes used for repeated requirement comparisons

Use `gregale secrets defaults set --environments staging,production` to save
only the comparison scope names, then run
`gregale secrets check --environments saved`. The comparison still resolves
required keys and app targeting through the existing selectors. It identifies
the absolute defaults source path in human output and JSON
`comparison_defaults_file`. Explicit scope lists bypass the saved file entirely.
A bare `secrets check` continues to check one scope; saving does not silently
change ordinary commands.

`secrets defaults show` displays the saved names and path; `clear` removes only
that local file and is safe when it is already absent. Set replaces the list,
trims whitespace, deduplicates in order, and accepts at most 10 distinct valid
scopes. None of these commands require login or API access or modify remote
configuration. Names are local preferences, not remote existence verification.

The file is `.gregale/secrets.json` at the nearest linked project root, otherwise
Git root, otherwise current directory, following the existing versioned Job
JSON-defaults convention. It contains version 1 and scope names only, with no
credentials or secret values. Writes are atomic and private (0600); malformed,
oversized, unsupported-version, or symlinked files fail when read. Show/set/clear
reject symlinked directory components and do not modify Job defaults, project
links, required-key declarations, or `.gitignore`. Clear can remove a malformed
regular defaults file so it can be replaced.

Only the exact `--environments saved` selector reads defaults. To check a literal
scope named `saved`, use `--scope saved`, or save it explicitly as a scope name
through the defaults set command. Other comparison, redaction, partial-evidence,
and exit-code behavior stays unchanged.

## Save a latest-request-error log view

Create a reusable error search with
`gregale logs views save recent-server-errors --latest-request-error --since 1h --route /checkout`.
Run it with `gregale logs --view recent-server-errors` (linked app) or
`gregale logs my-api --view recent-server-errors` (explicit app). The existing
saved-view store, show/list/delete commands, and `--replace` behavior are reused.
No app, request ID, or connection is pinned in the view: each invocation uses its
current app and connection and finds the newest recorded 5xx in the saved window.

The selector implies HTTP; optional `--source http` is accepted. Saved error
views accept only a positive relative `--since`, optional route, and replace;
status, limit, follow, grep, level, and runtime source are rejected. Existing
runtime and HTTP views retain their behavior and version-1 file compatibility.
The view stores a `latest_request_error` selector alongside its window/route,
not a second defaults file. Human execution identifies the saved view and
filters; normal selection output reports the effective retained window. JSON
keeps the latest-error selection receipt and canonical HTTP log record.

## Preview a linked-target change without saving

`gregale context set --environment production --dry-run` performs the same
read-only project/environment validation as a normal context update, then shows
the proposed before/after defaults, unchanged settings, and exact context file.
It never invokes the context writer. Omitted app/environment fields remain
unchanged. Clear-only previews such as `context set --clear-environment
--dry-run` remain local and require no login or API access.

JSON adds `dry_run: true` and `would_change`, with `changed: false` and `after`
containing the proposed defaults. Normal updates also report `would_change` and
retain their existing actual-change flag. A no-op preview reports no proposed
change. Existing argument, membership, identity, symlink, and changed-context
checks apply equally to previews. Run the update again without `--dry-run` to
save it; a successful preview does not reserve or guarantee a later validation.

## Understand and run saved log views from the list

`gregale logs views list` now describes each saved view's purpose: runtime logs,
HTTP requests, or latest HTTP error. The human list shows the relative window
and applicable route, level, grep, HTTP status/page size, and follow filters,
plus a POSIX run command using the view name and current connection profile.
Commands use the linked app; add `--app APP_SLUG` to select another app explicitly.
Listing never executes a view or makes API requests.

The sorted JSON list remains an array of names for compatibility. Use
`logs views show NAME --json` for the saved selector and full filter details.
Existing empty-list guidance, storage, and show/save/delete behavior remain
unchanged. Relative windows are recalculated when the view is run.

## Update one saved log-view filter

`gregale logs views update recent-server-errors --since 3h` changes the saved
window while preserving its selector, route, and omitted settings. Update
requires an existing view and validates the complete resulting definition before
saving. The source and latest-error selector stay unchanged; use `save --replace`
when intentionally rebuilding a view of another type.

Use `--clear-route`, `--clear-grep`, or `--clear-level` to remove a filter, and
`--follow=false` to turn off following. Normal HTTP views also support `--status
0` to clear status filtering and `--limit N` to change page size. Latest-error
views accept only window and route changes, keeping their existing filter rules.
Setting and clearing the same filter, empty operations, incompatible filters,
and extra arguments fail without modifying storage.

Human and JSON receipts show before/after settings, whether anything changed,
and the store path. Other views remain untouched. A no-op preserves the file;
changed views are re-read before the atomic private write to detect intervening
edits. Updating performs no API requests and never executes a view. Existing
save, show, list, delete, and JSON-list compatibility remain unchanged.

## Preview a saved log-view update

`gregale logs views update recent-server-errors --since 3h --dry-run` validates
the proposed view and shows its before/after filters and resulting run command,
without modifying storage or querying logs. Omitted fields, view type, explicit
clear options, and incompatible-filter validation follow normal update rules.
Use `show NAME` to inspect a view when no update is intended.

JSON adds `dry_run: true`, `would_change`, and `run_command`, with `changed:
false` and `after` containing the proposed definition. Normal updates also
include `would_change` and the command. The printed command uses inline proposed
filters rather than `--view NAME`, because that name still refers to the old
saved definition during a preview. It preserves the selected profile explicitly,
uses the linked app, and can take `--app APP_SLUG` for an explicit target.
Nothing executes automatically. Remove `--dry-run` from the update to save it.

## Rename a saved log view

`gregale logs views rename recent-server-errors production-errors` moves the
existing definition to an unused name, preserving every selector and filter.
Other views remain unchanged. Missing sources, invalid names, identical names,
and existing destinations fail without writing. Renaming re-reads the store
before the existing atomic private write to detect intervening edits.

Human and JSON receipts identify the old/new names, preserved definition, store
path, and run command with the selected profile. The command uses the linked
app; add `--app APP_SLUG` to select one explicitly. No app or environment is
changed by naming a view, and no log query or API request occurs. Existing
show/list/update/delete commands use the new name after a successful rename.

## Save and reuse linked target presets

After linking the desired targets, use `gregale context save staging` to capture
its project, app, and environment names locally. Existing names are refused
unless `--replace` is explicit. Preview restoration with
`gregale context use staging --dry-run`, then apply with
`gregale context use staging`. Both reuse normal context-update validation and
before/after receipts; preview writes no files. Empty preset fields explicitly
clear their defaults rather than preserving the currently selected target.

Presets are target names, distinct from connection profiles. Use operates on
the current connection and requires the same currently linked project; it does
not switch projects or credentials. For another project, relink explicitly
before applying its preset. Named apps and environments are validated through
read-only project reads before updating local defaults. Clear-only presets
remain local. Stored targets are not automatically inferred or guaranteed to
still exist when the preset is later used.

`context presets list`, `show NAME`, and `delete NAME` manage the local presets.
Saving and management require no login or API requests. Deletion does not alter
the current link. The version-1 `.gregale/context-presets.json` file follows the
project-root default convention, stores no credentials or connection settings,
is written atomically at mode 0600, and is bounded to 100 names/64 KiB. Malformed,
unsupported, or symlinked preset storage is rejected. Other project preferences
and `.gitignore` are unchanged. JSON apply receipts include `preset` and
`preset_path` alongside the existing proposed/actual-change fields.

## Update one saved context-preset target

`gregale context presets update staging --environment preview --dry-run`
validates the selected environment against the preset's project and displays
before/after targets without saving. Remove `--dry-run` to update only the
preset. The active project context is never modified or activated by this
command, including when the preset has another project name.

Omitted app/environment fields and the preset project remain unchanged. Named
app updates validate project workload membership; named environment updates
verify environment/project identity. These are read-only API calls on the
current connection under a 30-second validation deadline. Use `--clear-app` or
`--clear-environment` for explicit removals; clear-only updates are local and
need no login. Empty values, conflicting set/clear options, unknown presets,
and extra arguments fail without saving.

The receipt shows the preset path, proposed/actual-change flags, before/after
settings, and `active_context_changed: false`. A no-op preserves the file.
Before saving, the store is re-read and changed presets are refused; other
presets remain intact. Writes reuse the atomic private preset store. Applying
a preset later still requires `context use NAME` and its normal validation.

## Choose a preset from the target overview

`gregale context presets list` now marks presets that match the current linked
project/app/environment names, presets with different targets in the same
project, and presets that require relinking first. It displays the source file
and a copyable `context use NAME --dry-run` command with the selected profile.
Other-project or unlinked presets also show an explicit link command, labeled
as changing local defaults; no command is executed automatically.

Matches describe target names only, not an active-preset identity or remote
existence check. Multiple presets with identical targets can all match. Listing
makes no API requests and modifies no files or connection settings. JSON keeps
`path` and the existing `presets` map, adding sorted `entries` with target data,
local status, match flag, preview command, and any required relink command.

## Complete saved local names in the shell

Regenerate/reload the existing Bash, Zsh, Fish, or PowerShell completion script
to complete context preset names for `context use` and `context presets
show/update/delete`, and log-view names for `logs --view` (including `--view=`)
and `logs views show/update/delete/rename`. Rename completes only the existing
source name; new save names and rename destinations remain user-chosen.

Suggestions are sorted and prefix-filtered from the existing local stores,
without API calls, credential reads, prompts, or writes. Supported positions
with missing or malformed storage produce no suggestions and do not fall back
to remote name lookup. Preset discovery follows the current checkout's project
root; log-view names use the existing CLI configuration store. Profile selection
is preserved by the generated shell hooks. The internal context probe checks
only argument position without reading the stores.

The same offline completion also suggests locally recorded run UUIDs for
`jobs resume --run`, `jobs resume --forget`, `workflows resume-wait --run`, and
`workflows resume-wait --forget`, including `--run=UUID` and `--forget=UUID`.
Only records of the matching run type, current profile, and current API address
are suggested. Names are sorted, prefix-filtered, and deduplicated; no run is
selected or executed automatically. Missing or malformed records produce no
suggestions without remote fallback. Reload the generated shell script to use
these suggestions.

## Resume directly from an offline run listing

`jobs resume --list` and `workflows resume-wait --list` show a copyable resume
command for each saved run, including its recorded API address, profile, and
exact UUID. Each entry labels whether the recorded profile/API matches the
current connection; this is a local comparison, not a remote health or status
check. The same guidance appears when multiple records require an explicit
choice. Commands use POSIX shell syntax (`FAAS_API=... gregale ...`).

JSON listings remain arrays with the existing record fields and add
`connection_matches` and `resume_command` to each entry. Listing does not read
remote status, load credentials, change connection settings, or execute the
suggested command. Use `--overview` for current status and status-based actions.

## Recover by workload name

Use `jobs resume --job nightly-report --results` or
`workflows resume-wait --workflow order-processing --diagnose` to narrow saved
records by exact workload name and the current profile/API. A single match
uses the existing identity-checked recovery path. Multiple matches still
require an explicit `--run UUID`; zero matches fail before authentication or
remote requests. A combined name and UUID must match the same saved record.
Workflow names can repeat across apps, so name selection never chooses a latest run implicitly.

The same selectors work with `--list` and `--overview`, for example
`jobs resume --job nightly-report --list`. These modes show only matching
records on the current connection. Empty filtered lists/overviews remain valid
empty reports. Name filters cannot be combined with `--forget`, which continues
to require an exact UUID. Existing recovery without name filters is unchanged.

Use `jobs resume --saved-job --results` to select the Job name from the
project’s `jobs defaults`. It also supports `--logs`, `--list`, `--overview`,
and an explicit `--run UUID/PREFIX`. Only the name is reused: task counts,
artifact filters, and run preferences are not inherited. Multiple matching
runs still require `--run`; missing or invalid defaults fail before API access.
`--saved-job` cannot be combined with `--job` or `--forget`.
Run UUID completion also respects `--saved-job`: `jobs resume --saved-job
--run <TAB>` suggests only that Job’s recorded runs on the current profile/API.
Missing or invalid defaults produce no suggestions. Completion reads local
files only and never selects or executes a run.

Recovery completion also suggests saved workload names for `jobs resume --job`
and `workflows resume-wait --workflow`, including inline `--job=NAME` and
`--workflow=NAME`. After a name selector, `--run` suggestions include only that
workload's saved UUIDs on the current profile/API. Repeated name selectors use
the last value, matching command parsing. Names are exact, deduplicated, sorted,
and prefix-filtered. Spaces and shell characters are escaped by the generated
shell hooks; names containing tabs or line breaks are omitted from completion.
The argument-position probe remains read-free, and all suggestions stay offline.

## Declare required secret names without editing JSON

```bash
gregale secrets requirements set DATABASE_URL API_KEY
gregale secrets requirements show
gregale secrets check
gregale secrets requirements clear
```

`set` replaces `.gregale/required-secrets.json` with a JSON array of validated
key names, deduplicated in input order. It requires at least one name and
rejects `KEY=VALUE` pairs and flags. The declaration uses the nearest linked
project root, otherwise the Git root or current directory, matching existing
automatic discovery. Writes are atomic and private; symlink files or directories
are rejected. Setting names can replace a malformed regular declaration.

`show` is offline and reports names, the absolute source path, and whether a
declaration exists. It rejects malformed declarations; a missing file reports
unknown completeness. `clear` removes only the local declaration, is safe to
repeat, and can remove a malformed regular file. Missing declarations mean
unknown completeness, not an empty requirement set. Existing manually written
`[]` declarations remain readable as explicitly empty.

No command reads credentials, queries the API, or reads/writes secret values.
`secrets check` and `env push --dry-run` use the saved names automatically;
explicit requirements flags on `secrets check` still override discovery.
JSON output includes `keys`, `path`, `declared`, and `cleared` (whether a file
was removed by this invocation).

Edit individual required names without replacing the full list:

```bash
gregale secrets requirements add REDIS_URL
gregale secrets requirements remove OLD_API_KEY
```

`add` preserves existing order and appends new names once; adding only existing
names leaves the file untouched. It creates a declaration if none exists.
`remove` checks that every requested name exists before changing anything and
removes all occurrences of those names. Removing the final name saves `[]`, an
explicitly empty declaration, rather than deleting the file. Both operations
reject malformed existing declarations and use the same validated, atomic,
private writes and 64 KiB limit. They do not query the API or change secret
values. JSON receipts additionally include `changed`; repeated duplicate adds
report `false`. `clear` still removes the declaration, meaning completeness is
unknown.

## Recover with a short saved run ID

`jobs resume --run 8ac1f2d0 --logs` and
`workflows resume-wait --run 3bd420aa --diagnose` accept a unique prefix of a
locally saved UUID. Prefixes require at least eight characters and must match
the lowercase, hyphenated UUID format from its beginning. Full UUIDs remain
accepted. This applies to observation, Job results, logs, and Workflow diagnostics.

Resolution uses only matching run-type records on the current profile/API,
plus any `--job` or `--workflow` selector. A single match supplies the full saved
UUID to existing identity-checked API reads. Human output reports the resolved
UUID. Multiple matches show only matching saved records and their full recovery
commands, then fail before authentication or API access; use a longer prefix or
full UUID. No match also fails before API access. The CLI never chooses by age
or remote status. Empty `--run` values are rejected. Completion still suggests
full UUIDs. `--forget` continues to require a full UUID, and `--run` cannot be
combined with listing, overview, or forgetting modes.

Shell completion for `secrets requirements remove <TAB>` suggests names from
the existing project declaration. Suggestions are sorted, prefix-filtered,
deduplicated, and exclude names already entered earlier in the command. The
same inherited project-root discovery applies from subdirectories. Missing,
empty, malformed, oversized, or symlink declarations produce no suggestions
and do not trigger remote lookup. Completion does not read secret values or
change the declaration. Reload the generated shell completion script to use it.

Preview required-name edits with `--dry-run`, before or after the key names:

```bash
gregale secrets requirements set DATABASE_URL API_KEY --dry-run
gregale secrets requirements add REDIS_URL --dry-run
gregale secrets requirements remove OLD_API_KEY --dry-run
```

The offline preview shows the destination path, current/proposed ordered names,
and names added or removed. It distinguishes a missing declaration (unknown
completeness) from an explicit empty declaration. Removing the last name shows
that `[]` would be saved and the file retained. Duplicate adds report no change.
Nothing is written, including when the declaration directory does not yet exist.

Preview requires a valid existing declaration to report an accurate before/after;
malformed, oversized, symlink, or directory declarations are rejected. Normal
`set` can still replace a malformed regular declaration. Unknown removal names
and invalid input fail before writes or API access in both preview and normal
edits. JSON previews include `path`, `operation`, `dry_run: true`, `changed: false`,
`would_change`, `before_declared`, `before`, `after`, `added`, and `removed`.

## Import required names from a dotenv example

```bash
gregale secrets requirements set --from-env-file .env.example --dry-run
gregale secrets requirements set --from-env-file .env.example
```

`--from-env-file PATH` (or `--from-env-file=PATH`) is supported on `set` and `add`
and excludes positional names. Paths resolve relative to the current working
directory. The import skips blank/comment lines, accepts single-line `KEY=VALUE`
assignments and optional `export ` prefixes, trims whitespace around keys, and
deduplicates names in first-seen order. Empty values are valid. Values are opaque
text: they are never expanded, executed, stored in the declaration, or printed.
Multiline values and shell scripts are not supported; lines without valid key
assignments are rejected with line-number-only diagnostics.

Inputs must be regular, nonsymlink files, at most 1 MiB with lines below 64 KiB;
empty/comment-only files, NUL bytes in assignments, invalid keys, and oversized
resulting declarations fail before writing. The existing 64 KiB requirement
limit, project-root discovery, private atomic writes, and offline preview apply.
JSON save/preview receipts include `source_file`, the absolute input path.
Preview reads the source but does not create directories or modify either file.
All imported keys become required names; review the preview before saving.

Merge imported names into an existing declaration with
`secrets requirements add --from-env-file .env.example --dry-run`, then remove
`--dry-run` to save. Existing names retain their order, new names append in
first-seen input order, and duplicates are skipped. An import containing only
existing names reports no change and leaves the file untouched. A missing
declaration is created by a normal add, while preview creates nothing. The
same source validation, limits, names-only receipts, and malformed-declaration
rejection apply. File import is supported on `set` and `add`, excludes positional
names, and remains unsupported on `remove`.

## Check required-name drift offline

```bash
gregale secrets requirements check --from-env-file .env.example
gregale --json secrets requirements check --from-env-file .env.example
```

The check compares name sets, ignoring order and duplicates, without writing
files or contacting the API. It reports names in the example but missing from
the declaration, and declared names absent from the example. Exit 0 means a
valid declaration has the same name set; differences, an undeclared project,
or invalid input return nonzero. This can be used in CI. It does not assess
whether remote secrets exist or whether secret values are correct.

Missing names include a copyable, profile-preserving merge preview command.
Extra declarations include a separate removal preview for review; merging does
not remove them, and absent example entries may still be intentionally required.
A missing declaration reports unknown completeness. An explicit `[]` is a valid
empty declaration and will differ from a nonempty example. Empty/comment-only
examples remain invalid, consistent with import validation.

The existing dotenv/declaration bounds and guarded readers apply. JSON includes
`declared`, `consistent`, `path`, `source_file`, `required_names`, `example_names`,
`missing_requirements`, `extra_requirements`, and applicable `merge_command` or
`review_remove_command`. Commands use POSIX shell syntax and preview only. No
values are emitted, no files are created, and invalid/malformed declarations
are rejected rather than treated as empty.

The consistency check also works without flags:

```bash
gregale secrets requirements check
```

It uses `.env.example` at the same project root as the saved declaration,
including when invoked from a subdirectory. A nested `.env.example` does not
replace the project's example. `--from-env-file PATH` overrides this default;
relative override paths resolve from the current directory. An explicitly empty
path is rejected. If the project example is missing, the command reports the
expected absolute path and how to select another file, then exits nonzero.
It does not search for real `.env` files or create an example. JSON adds
`source_selection` (`project_default` or `flag`) alongside `source_file`.

## Inspect all saved project configuration offline

`gregale context --local-config` shows the resolved project root and the absolute
paths/status of `.gregale/jobs.json`, `secrets.json`, `required-secrets.json`, and
`context-presets.json`. It uses the same nearest-linked-project, Git-root, then
current-directory discovery as each existing command, including from subdirectories.
Each row includes a profile-preserving inspect command. Invalid regular files
with an existing clear command also show how to explicitly discard that local
configuration; symlinks/directories and preset files require manual review.
Nothing is cleared or repaired automatically.

Missing optional files are reported and do not fail the summary. Required names
missing means completeness unknown; a valid `[]` is explicitly empty. Existing
files use their established validators and size limits. Invalid, unreadable,
oversized, or unsafe files fail the summary while the remaining rows are still
reported. Raw malformed contents and parsing details are omitted. If a malformed
project link prevents resolving the root, fix that link before retrying.

The flag is used separately from `--check`, `--verify`, and `--timeout`. It reads
no credential values, makes no API requests, and writes no files. It does not
claim remote validity or health. JSON output contains `root`, `valid`, and a
`files` array with `name`, `path`, `status` (`configured`, `missing`, `invalid`),
`summary`, `inspect_command`, and applicable `repair_command` fields.

## Preview saved Job defaults offline

```bash
gregale jobs defaults set nightly-report --tasks 4 --results --dry-run
gregale jobs defaults update --tasks 3 --dry-run
```

Both `set` and `update` support offline previews before saving. Output includes
the absolute destination path and current/proposed Job name, task count, results
preference, image-wait preference, and artifact filters. `set` proposes a complete
replacement; `update` preserves omitted settings and still requires valid saved
defaults. Normal validation applies, including positive explicitly supplied task
counts and valid artifact filters.

Preview never creates a directory, writes a file, queries the API, or submits a
Job. Missing defaults can be previewed with `set`. Malformed, oversized (64 KiB),
symlink, or directory defaults are rejected so the before/after remains accurate;
normal `set` can still replace a malformed regular declaration. Matching settings
report no change. JSON includes `path`, `dry_run: true`, `changed: false`,
`would_change`, `before_saved`, `before` (null if absent), and `after`.

Unset only the saved task count with
`gregale jobs defaults update --clear-tasks --dry-run`, then remove `--dry-run`
to save. The Job name, results preference, image-wait preference, and artifact
filters remain unchanged unless explicitly updated in the same command.
Unset means no saved task-count override; it does not force zero tasks on a run.
`--clear-tasks` is supported on `update` only and conflicts with any explicit
`--tasks` value. Explicit `--clear-tasks=false` does not clear the count and
requires another update operation. Already-unset counts preview as no change.

Edit saved artifact filters incrementally with repeatable `--add-include`,
`--remove-include`, `--add-exclude`, and `--remove-exclude` flags on
`jobs defaults update`. For example:

```bash
gregale jobs defaults update --add-include '*.csv' --dry-run
gregale jobs defaults update --remove-exclude 'private*' --dry-run
```

Existing filters and other defaults are preserved. Additions append in input
order and skip duplicates. Removals match the exact saved pattern, not artifacts
matching that glob, and remove all occurrences. Unknown removals, malformed
patterns, and adding/removing the same pattern fail without writing. Incremental
edits conflict with replacement `--include`/`--exclude` and `--all-artifacts`.
Both lists can be edited together; a failure in either prevents the entire save.
Use `--dry-run` to inspect the complete proposed defaults before saving. Removing
all includes permits all artifact names subject to remaining excludes; removing
all excludes stops filtering artifacts out by those patterns. No API calls or
run submissions are made.

Shell completion for `jobs defaults update --remove-include <TAB>` and
`--remove-exclude <TAB>` suggests exact saved patterns from the corresponding
list, including inline `--remove-include=PATTERN` / `--remove-exclude=PATTERN`.
Suggestions are sorted, deduplicated, prefix-filtered, and omit patterns already
selected for removal from that same list. A removal in the other list does not
hide the pattern. The existing shell hooks escape spaces and glob characters.
Patterns containing tabs or line breaks are omitted. Missing, malformed,
oversized, or unsafe defaults produce no suggestions or remote fallback. This
is offline, does not change files, and follows inherited project-root defaults.
Reload your generated shell completion script to use it.

Saved Job defaults now show the next run-preview step after `set`, `update`, and
`show`. When a positive task count is saved, output includes a copyable,
profile-preserving `gregale --profile PROFILE jobs run JOB --dry-run` command.
It uses the defaults saved in this checkout, including results, image-wait, and
artifact preferences. Generating this guidance is offline. Executing the run
preview may query API metadata but does not submit a Job.

If tasks are unset, output explains that a positive task count or explicit input
bindings are required and labels `--tasks N` as a template. It does not invent a
task count or present an incomplete command as ready to run. JSON preserves
`defaults` and `path` and adds `next_step`, containing `preview_command` or
`preview_command_template`, `needs_task_count_or_inputs`, and `note`. These
commands use POSIX shell quoting. Unsaved defaults previews continue to show
proposed settings without claiming they are active defaults.

Preview removal with `gregale jobs defaults clear --dry-run`. It shows the
absolute file path, saved settings, and the effect on omitted Job selectors,
task/results/image-wait preferences, and artifact filters. Missing defaults
report nothing to clear and exit successfully, without creating directories.
The preview makes no writes, API requests, or run submissions.

An existing declaration must be valid, regular, nonsymlink, and within 64 KiB
so its contents can be accurately summarized. Malformed or unsafe files fail
without removal. Normal `clear` remains available for removing malformed local
defaults. JSON includes `dry_run: true`, `changed: false`, `cleared: false`,
`would_change`, `before_saved`, `before`, `after: null`, `path`, and `effects`.
Without `--dry-run`, the existing clear behavior and JSON receipt are preserved.

Rename a context preset offline with
`gregale context presets rename staging staging-eu`. It preserves the preset's
project/app/environment targets and all other presets without applying or
changing the active context. Unknown sources, invalid or identical names, and
existing destinations are rejected without changes. Malformed/unsafe stores
fail through the existing guarded reader. The store is re-read before the
private atomic write to detect intervening changes.

The receipt includes `from`, `to`, `preset`, absolute `path`,
`active_context_changed: false`, and a profile-preserving `inspect_command`
using the new name. Source-name tab completion is available; destination names
remain user-chosen. No API queries, credential changes, or prompts are involved.

Create a similar preset without applying it first:

```bash
gregale context presets copy staging staging-eu
gregale context presets update staging-eu --environment staging-eu --dry-run
```

Copy preserves the source preset, duplicates its complete project/app/environment
selection, and leaves all other presets and the active context unchanged. It
uses the same offline validation, re-read checks, private atomic writer, and
64 KiB/100-name limits as preset management. Identical/invalid names, missing
sources, existing destinations, malformed/unsafe stores, and limit overflow
fail without changes. No API calls or prompts occur during copy; a subsequent
named-target update uses its existing read-only remote validation.

Source-name completion is available; new names remain user-chosen. Copy and
rename receipts include `operation` and `source_preserved` in addition to
`from`, `to`, `preset`, `path`, `active_context_changed`, and `inspect_command`.
Copy reports `source_preserved: true`; rename reports false.

Preview a preset deletion with
`gregale context presets delete staging --dry-run`. It displays the preset's
project/app/environment selection and absolute source file, confirming that
active context and other presets would remain unchanged. `--dry-run` can appear
before or after the name. Unknown names, invalid arguments, and malformed or
unsafe stores fail without writes. The store is re-read to check for changes
before preview or deletion. No API requests or prompts are involved.

JSON previews include `name`, `preset`, `path`, `dry_run: true`,
`changed: false`, `would_change: true`, and `active_context_changed: false`.
Normal deletion and its `deleted`/`path` receipt remain available by omitting
`--dry-run`. Preview does not remove a preset, create directories, or update
active targets.

Copy a saved log view with `gregale logs views copy errors checkout-errors`,
then adjust only the copied view with `logs views update`. Copy preserves the
source and every saved filter: source, relative window, latest-error selector,
route/status/limit, and runtime filters/follow preferences. Route edits remain
limited to HTTP views; copying does not convert a runtime view to HTTP.
Existing destinations, identical/invalid names, missing sources, and malformed
or symlink stores are rejected without changes. Copy is offline, re-reads the
store before the private atomic save, and does not query logs or change targets.

Source-name completion is available; destination names remain user-chosen.
Receipts include a profile-preserving `run_command` for the new name. Copy and
rename also report `operation` and `source_preserved`, alongside their existing
`from`, `to`, `view`, and `path` fields. Copy reports true and rename false.

Preview saved log-view deletion with
`gregale logs views delete checkout-errors --dry-run`. It shows the view's
source type, filters, and absolute storage path, while confirming that other
saved views would remain unchanged. The flag can appear before or after the
name; name completion also supports the flag-before-name form.

Unknown/invalid names, malformed or unsafe stores, and invalid arguments fail
without deleting anything. The store is re-read before preview or normal
deletion to detect intervening edits. Preview makes no writes, directory
creations, API requests, or log queries. JSON includes `name`, `view`, `path`,
`dry_run: true`, `changed: false`, `would_change: true`, and
`other_views_changed: false`. Omitting `--dry-run` retains normal deletion and
its existing `deleted` receipt.

## Get typo suggestions for saved names

Unknown context preset or log-view names now suggest up to three nearby saved
names and corrected, profile-preserving commands. This covers `context use`,
preset/view show and update, copy/rename sources, deletion, and `logs --view`.
For example, `context use stagng --dry-run` can suggest `staging` while keeping
the preview flag. Log-view suggestions preserve app targeting; copy/rename
suggestions preserve the destination name. Arguments are quoted for POSIX shells.

Suggestions use only the already-validated local store, rank by edit distance
then name, and stay within a small typo threshold (one edit for short names,
two for longer names). Empty or very long queries and distant matches do not
produce guesses; the error still provides the list command. No suggestion is
selected, executed, or written automatically. Unknown-name commands remain
nonzero and stop before remote reads or writes. Malformed storage remains a
storage error rather than providing guesses. JSON errors remain standard
problem diagnostics, with suggestions in the error detail.

Saved-run recovery also suggests nearby workload names when a `--job` or
`--workflow` selector matches no recorded run. For example,
`jobs resume --job nigtly-report --results` can suggest `nightly-report` with a
corrected command. Candidate names come only from the matching run type on the
current profile/API. Duplicated names across runs produce one suggestion.
Correction commands preserve the supplied evidence/results mode, timing flags,
and run UUID/prefix. Names with control characters are not suggested.

Unknown selectors stop before authentication, remote reads, or downloads; no
suggestion is selected or executed automatically. Existing exact-match recovery
and ambiguous-run handling are unchanged. `--list` and `--overview` continue to
return valid empty reports for selectors with no matches, without typo guesses
or changing their exit semantics. Malformed run storage remains an error.

## Explicitly run the saved Job

`jobs run --saved-job --dry-run` explicitly uses the Job name and omitted
settings from `jobs defaults`, just like the existing name-omitted invocation.
Explicit flags override saved settings; explicit inputs suppress the saved
task count. Supply `--tasks N` or inputs when neither is configured.
`--saved-job` cannot be combined with a positional Job name. Missing or invalid
defaults fail before API access. Remove `--dry-run` to submit through the
existing run path; preview never submits a run.

## Explain run preview settings

`jobs run --saved-job --dry-run` now includes a setting-source summary.
It distinguishes saved Job defaults, explicit names/flags, CLI defaults,
input bindings, and implications such as result collection enabling waiting.
Remote Job defaults and input manifest contents remain marked as not read.
JSON previews add `setting_sources` alongside the existing effective values;
`task_count_source` continues to describe how tasks are counted. Sources are
captured while applying defaults, without additional file reads or API calls.
Environment values, arguments, and input references remain redacted.

## Inspect the configured run target

Run previews show the active profile and configured API address, including
their sources, and the absolute local Job defaults file path. JSON adds
`connection` (with `verified: false`) and `local_defaults` (path, saved,
matches_job, applied). Defaults for a different Job are explicitly shown as
not matching or applied; a missing file is shown as not saved. This metadata
comes from the same defaults read used to prepare the run.
No credentials or remote access are checked. URL credentials, query strings,
and fragments are omitted from API display (`address_redacted: true`);
malformed addresses are hidden. The configured connection remains unchanged.

## Preserve the requested action when choosing a run

When recovery requires an explicit run choice, matching records now include
a command retaining the requested action, workload selector, and explicitly
provided timing flags. For example, `jobs resume --saved-job --results` with
multiple matches shows a command for each exact UUID with those options.
An ambiguous `--run` prefix is replaced with each full UUID. Workflow diagnosis
and Job logs use the same guidance. Commands pin the current matching API
and profile, are POSIX shell quoted, and are never executed automatically.
JSON choice listings add `selection_command` for matching connections; existing
`resume_command` remains observation guidance. Records from other connections
retain observation guidance only. Ordinary `--list` output is unchanged.
Choosing a run still returns nonzero before authentication or remote requests.

## Check saved recovery records locally

`context --local-config` also inspects `.gregale/accepted-runs` at the same
project root. It reports valid Job/Workflow record counts across all recorded
connections and identifies malformed, oversized, invalid, or unreadable JSON
files by absolute path without displaying their contents. It continues after
individual bad files, and returns nonzero if any record or directory is invalid.
Missing and empty directories remain valid. All records are retained.
JSON adds `recovery_records`, with status, counts, scanned_records, scan_complete,
issues, and inspect_commands. Counts cover validated records only. Scans are
bounded to 1000 JSON records and 10000 directory entries; incomplete scans are
marked invalid with partial counts. Valid nonempty stores offer Job/Workflow
recovery-list commands on the current profile. No remote status or credentials
are read and no repair is performed automatically.

## Limit recovery reports to the current connection

Use `jobs resume --list --current-connection` or
`workflows resume-wait --overview --current-connection` to include only records
whose saved profile and API match the current connection. Profile and API
must both match, including an effective `FAAS_API` override. The flag requires
`--list` or `--overview`; it can be combined with workload selectors or
`--saved-job`. `--current-connection=false` leaves the existing report scope
unchanged. Empty matching reports are valid.
Listings remain offline. Overviews read status only for included records;
filtering does not verify credentials or remote access. All local records
remain unchanged, including excluded records. Malformed local records still
fail validation rather than being hidden by the filter.

## Find recovery records for the active connection

`context --local-config` now shows both total validated recovery counts and
counts matching the current profile/API. JSON `recovery_records` adds
`current_connection_jobs` and `current_connection_workflows`. Both the profile
and effective API must match, including any `FAAS_API` override.
On valid stores, inspection suggestions use `--list --current-connection`
and appear only for workload types with matching records. Counts remain local
comparisons, not remote status checks; partial or invalid scans retain their
existing diagnostics and do not offer inspection commands.

## Preview a log query offline

Use `logs --view errors --dry-run` or
`logs --app my-api --source http --status 500 --since 1h --dry-run` to inspect
the effective app, source, and validated filters without authentication or
remote reads. Human and JSON output distinguish explicit flags/names, saved
view filters, linked app targeting, CLI defaults, and inferred selection rules.
Saved views accept an app target, `--dry-run`, and compatible one-off
filter overrides. No interactive picker is opened by a preview.
Logs are app-scoped: a linked environment does not filter these queries.
Runtime lookbacks are normalized to timestamps; HTTP lookbacks retain their
query duration. Deployment revisions/latest references, server defaults,
retention, and runtime/build-log routing remain unresolved. Archive and
latest-failure/latest-request-error selectors can also be previewed without
selecting remote records. JSON includes dry_run, app, profile, environment_scope,
mode, source, filters, setting_sources, notes, and optional saved view metadata.
The existing log-query validation still applies. Omit `--dry-run` to execute
the query through its existing path.

## Override saved log filters for one query

`logs --view errors --since 15m --dry-run` uses the saved view with an explicit
window override. Remove `--dry-run` to execute the same effective query; the
saved view is never rewritten. Runtime views support since, grep, level, and
follow overrides; ordinary HTTP views support since, route, status, and limit.
Latest-request-error views support only since and route. App targeting remains
independent. View source and selection type cannot be changed.
Explicit empty `--grep=`, `--level=`, or `--route=` clears that filter for
this query; `--follow=false` disables saved follow mode. Existing query validation
still applies, including HTTP status 100..599 and page limits 1..200. Temporary
windows may use RFC3339 timestamps as well as relative durations. Previews mark
overridden values as explicit_flag while inherited filters remain saved_view.

## Temporarily remove a saved HTTP status filter

`logs --view http-errors --clear-status --dry-run` previews the saved query
without restricting it to the saved status code. Remove `--dry-run` to query
all statuses with the remaining filters. The saved view is unchanged.
`--clear-status` requires an ordinary HTTP view, excludes explicit `--status`,
and is unavailable for runtime or latest-request-error views.
`--clear-status=false` keeps the saved status filter. Clearing an already
unfiltered HTTP view is valid. Preview JSON marks status as explicit_flag,
uses `filters.status: 0` for no status restriction, and adds
`view.cleared_filters: ["status"]`; human output labels it as all HTTP statuses.

## Clear saved text filters for one log query

`logs --view errors --clear-grep --clear-level --dry-run` removes saved
text and level restrictions from the preview. `--clear-route` removes a saved
route restriction from ordinary HTTP or latest-request-error views.
These options require a compatible saved view, conflict with their corresponding
explicit filter flags, and leave the saved file unchanged. False clear flags
leave the saved filters in place. Clearing an already-empty filter is valid.
Preview JSON lists cleared fields under `view.cleared_filters`, with empty
filter values marked explicit_flag; human output labels the cleared restrictions.
Empty overrides such as `--grep=` still work. Omit `--dry-run` to query through
the same validated path with the saved restrictions removed for that invocation.

## Copy the effective log query from a preview

Log previews now include a POSIX-shell quoted `query_command` in JSON and
a copyable human command. It pins the app and selected profile, expands
saved filters, applies overrides/clears, and omits view, clear, and dry-run
flags. Changes to the saved view cannot change the copied query’s filters.
Relative windows stay relative and are recalculated when the command is run;
explicit timestamps and unresolved deployment references are preserved.
Runtime, HTTP, archive, and latest-error selectors keep their query mode.
The command uses the selected profile’s configured connection with any API
environment override still applying; no connection or remote resource is
verified by the preview. Commands are shown for review and never executed
automatically.

## Validate archived-log instance IDs before querying

Archive queries and `--dry-run` previews validate `--instance` locally using
the server’s exact format: 1..128 ASCII letters, numbers, dots, underscores,
or hyphens. IDs do not have to be UUIDs. Spaces, slashes, non-ASCII characters,
and longer IDs fail with format guidance before app selection, authentication,
or network access. The instance value is not trimmed or rewritten; valid
IDs and UTC-date behavior are unchanged. Local format validation does not
establish instance ownership, archive availability, or retention.

## Use UTC archive date shortcuts

`logs --archive --instance INSTANCE --date yesterday --dry-run` resolves the
previous UTC day locally. `--date today` selects the current UTC day.
Previews show the resolved `YYYY-MM-DD` date, and the copyable query command
pins that date rather than retaining the shortcut. Without a preview, the
shortcut resolves when the query starts. Local timezone settings do not change
the result. Existing explicit `YYYY-MM-DD` dates remain supported and validated;
invalid dates fail before authentication or network access. Format validation
does not establish archive availability or retention.

## Preview archives from the instance history list

`ps --all` now includes copyable archive-preview commands for each instance
with a valid archive ID. Each command pins the app and current profile and
uses `--date yesterday --dry-run`; running the preview resolves the previous
UTC day locally without reading an archive. Instance listing does not establish
archive availability, retention, or plan access.
JSON remains NDJSON and preserves existing instance fields, adding
`archive_preview_command` and `archive_availability: "not_checked"` when
`--all` is used. Commands are omitted for IDs or app targets that fail archive
query validation. Ordinary `ps` output, empty-list behavior, and the history
cap are unchanged. The listing makes no additional API calls and executes
none of the suggested commands.

## Complete filters compatible with the selected log view

After `logs --view errors --`, shell completion reads the selected view locally
and suggests compatible runtime or HTTP filters and clear flags. Latest-error
views suggest only since/route filters plus app, view, and dry-run options.
Both `--view NAME` and `--view=NAME` work; repeated selectors use the last name.
Flag values are consumed as data and are not treated as view selectors.
Unknown, missing, malformed, or unreadable saved views fall back to ordinary
completion. Value completion remains unchanged. Bash, Zsh, Fish, and PowerShell
use the shared local helper; no credentials or remote API calls are needed.
Reload or regenerate the Fish completion script to enable its static-flag
suppression rule for recognized saved-view flag positions.

## Avoid conflicting log-view options during completion

Saved-view flag completion now omits a filter when its clear flag is enabled,
and omits the corresponding clear flag when the filter was explicitly provided.
This applies to grep, level, route, and status. Explicit empty filter values
count as supplied. `--clear-grep=false` allows grep suggestions again; repeated
clear flags use their last boolean value, matching command parsing.
Only completed flags are considered, and values that resemble flags remain
data. Saved filters alone do not suppress overrides or clears. Command-side
validation remains authoritative, and completion stays offline.

## Preview directly from saved-view listings

`logs views list` now shows a copyable preview command beside each query
command, for example `gregale --profile default logs --view errors --dry-run`.
Listing is offline and never runs the suggested commands. Previews use the
linked app or an explicit app override and do not verify remote access.
`gregale --json logs views list --details` returns sorted objects with name,
view, run_command, and preview_command. The existing JSON names array remains
the default (`--details=false` also retains it). Empty detailed listings are
empty arrays. Each command retains the selected connection profile.

## Target saved-view listing commands at an app

`logs views list --app my-api` includes the explicit app in each query and
preview command, without resolving project links or calling the API.
`--app` must be a valid nonempty slug; it is not saved into the view.
`--details` JSON entries add the explicit app (when supplied) and
`app_verified: false`. The default JSON names array remains unchanged even
with `--app`. Without the flag, generated commands retain their existing
linked-app behavior. Listing and preview do not establish app existence or access.

## Complete the app target in a saved-view listing

`logs views list --app <TAB>` and `logs views list --app=prefix<TAB>` now
use the shared local completion helper to suggest cached app slugs. The inline
form retains the `--app=` prefix. Suggestions are validated, sorted, and
deduplicated; `--details` may precede app targeting.
The current connection’s credential-bound cache is used, or the explicit
`FAAS_COMPLETION_CACHE_PATH` override. No remote refresh or app verification
is performed. Missing, unreadable, malformed, oversized, or unsupported-version
caches produce no suggestions. The context probe does not read the cache.
Reload generated completion scripts, especially Fish, for the updated rules.

## Inspect one view with targeted preview guidance

`logs views show errors --app my-api` shows saved filters plus copyable query
and preview commands for that explicit, unverified app. Without `--app`, the
commands use linked-app targeting. App flags may precede or follow the view
name and are validated locally; no API calls or saved-file changes occur.
`--details` JSON uses the same name/view/app/app_verified/run_command/
preview_command fields as detailed listings. The default JSON saved-filter
object is unchanged, including when an app target is supplied. Unknown-name
corrections retain app targeting and details options.

## Complete a view name after show options

`logs views show --app my-api --details <TAB>` now completes saved view
names after the completed options. Inline app values and boolean details
values work too; an unfinished app value is left to app completion.
Only the first view-name position is completed. Option values are consumed
as data, so flag-looking values cannot become options or view names.
The existing name-first form remains supported. Completion stays offline,
and its context probe does not read saved files.

## Complete app targets while inspecting one saved view

`logs views show errors --app=prefix<TAB>` and the spaced `--app <TAB>` form
now share the listing’s bounded, validated local cache reader. Options may
appear before or after the view name, and `--details` is supported. The helper
accepts at most one completed view name and consumes app values as data.
Cache isolation, sorted/deduplicated suggestions, missing/invalid-cache behavior,
and the read-free context probe are unchanged. Completion performs no API calls.

## Find saved views by log source

`logs views list --source http --app my-api` lists only HTTP views, including
latest-request-error views. `--source runtime` selects runtime views. Filtering
works in human output, the default JSON names array, and `--details` JSON entries.
App-targeted commands and their preview metadata remain available; source
filtering does not change any stored view. Omit --source to list all views.
Only runtime/http are accepted, and empty matches remain valid empty arrays
with explanatory human guidance. Listing remains offline; the full saved store
is still validated. App completion continues to work after source options.

## Find saved log views by part of their name

`logs views list --name checkout` selects views whose names contain that
literal, case-sensitive text. Combine it with --source, --app, and --details;
name and source filters both must match. JSON retains its sorted names-array
or detailed-entry shape, and empty matches are valid empty arrays.
No glob or regular-expression matching is performed. Empty or blank-only
filters are rejected; omit --name to list without a name filter. Repeated
filters use their last value. Listing remains offline and read-only, and
app completion works after completed name filters.

## Inspect the configured connection in log previews

`logs --view errors --dry-run` now includes configured profile/API details
with their sources, including a `FAAS_API` override. JSON adds `connection`
with profile, profile_source, api_base, api_source, verified (false), and
address_redacted, while preserving the existing profile field.
The same shared offline formatter is used by Job previews. API URL credentials,
query strings, and fragments are hidden; malformed addresses are not displayed.
Configuration is not changed, and no credentials, app access, API health, or
remote logs are checked. Runtime, HTTP, archive, and latest-error previews all
include this metadata. Copyable commands retain their existing profile targeting
and API-environment-override behavior.

## Reuse a project-specific preferred log view

Configure an existing saved view with `logs defaults set errors`, then preview
it with `logs --saved-view --dry-run`. `logs defaults show` reports the stored
name, absolute file path, and preview command; `logs defaults clear` removes
only the preference and is idempotent. The versioned `.gregale/logs.json` stores
only the view name, using the same nearest project/Git/cwd root as Job defaults
and a private atomic write. Readers reject malformed, unknown-field, oversized
(over 8 KiB), symlink, or nonregular defaults files.
The selector is opt-in: plain logs and explicit `--view` do not read or apply
the preference. `--saved-view` excludes `--view`, other latest selectors, and
interactive selection. Compatible overrides and clear flags work as before.
Missing preferences or deleted/renamed referenced views fail locally with
reconfiguration guidance; no alternate view is selected automatically.
Current global view filters are loaded when used, and existing app/profile/API
targeting remains unchanged. Preview metadata includes `view.defaults_path`
and marks the view selection as project_defaults, while filter origins remain
saved_view or explicit_flag. Copyable query commands expand the effective
filters and do not depend on the preference. Local configuration summaries
include the new defaults file. Default-set name completion and saved-view
compatible flag completion use local files only.

## Check the preferred view reference before querying

`logs defaults show` now reports whether the preferred view exists in the
local saved-view store, its source and latest-error mode, and the saved-view
file path. JSON adds `reference` with status available, missing, or unreadable.
Missing references include a list command and a clearly labeled reconfiguration
template; unreadable stores identify the file to review without displaying
invalid contents. Preview commands are offered only for available references.
Show returns nonzero for missing/unreadable references while retaining the
structured report. Missing preferences still report not saved successfully.
Set receipts and clear behavior are unchanged. All checks are local; no view
existence or availability is established on the remote API, and no defaults
or view files are rewritten automatically.

`context --local-config` includes the same reference check whenever a valid
log preference is saved. Its log-defaults row keeps the preference file and
saved-view store separate: `configured` means the reference is available;
`broken_reference` means the preference file is valid but its view is missing
or the store is unreadable. JSON includes the `reference` details, sets `valid`
to false, and returns nonzero for broken references. Human output shows the
store path, inspection command, and replacement template when applicable.
Missing optional preferences remain successful, and invalid preference files
retain their existing validation guidance. This check is offline and read-only.

## Preview a preferred log view before saving

Run `gregale logs defaults set errors --dry-run` to inspect the resolved
preference path, before/after view names, and selected filters without writing
files. The copyable log-preview command uses `--view errors` directly, so it
works before the preference is saved; add `--app SLUG` if no app is linked.
Flags can appear before or after the name. JSON reports `dry_run`, `changed`
(false), `would_change`, `before_saved`, `before`, `after`, `filters`, and
`preview_command`. Matching preferences report no change needed. Existing
malformed or unsafe preference files must be reviewed before previewing.
The command reads local configuration only and does not check remote app access
or guarantee that the destination is writable. Omit `--dry-run` to save.

## Show only local configuration problems

Use `gregale context --local-config --problems-only` for a compact check of
invalid configuration files, broken preferred-log-view references, and damaged
or incompletely scanned recovery stores. The same full offline validation runs
before filtering, and the exit status is unchanged. Missing optional files and
healthy entries are omitted. A healthy project prints “No local configuration
problems found”; remote resources and health are still not checked.

JSON includes `problems_only: true`, the project `root`, the overall `valid`
result, and a `files` array containing only problem entries. `recovery_records`
is included only when that store has a problem. Healthy reports contain an
empty `files` array. Inspection and explicit repair guidance is retained, and
no files are changed. `--problems-only` requires `--local-config`; omit it for
the complete inventory.

## Preview clearing a preferred log view

`gregale logs defaults clear --dry-run` shows the resolved preference path,
current view name, and the effect of removing the preference. It does not
delete files or read the global saved-view store, so deleted views and broken
view stores do not prevent this preview. Missing preferences succeed with
“nothing to clear.” JSON reports `dry_run: true`, `changed: false`,
`cleared: false`, `would_change`, `before_saved`, `before`, `after: null`,
and `effects`. Existing malformed, oversized, or unsafe preference files are
rejected without displaying their contents. Ordinary clear can still explicitly
remove a malformed regular preference file. Omit `--dry-run` to remove the
preference; saved views remain available via `logs --view NAME`. All preview
checks are offline and read-only.

## Inspect preferred log filters in one command

`gregale logs defaults show` now includes the preferred view's current saved
filters: its relative time window and active runtime or HTTP filters. The
existing source and latest-error mode identify the query type. JSON adds
`filters` using the saved-view format, only when the reference is available.
The filters and reference status come from the same validated local snapshot;
editing a saved view is reflected on the next show. Missing or unreadable
references retain their repair guidance and do not display filters or a preview
command. Set receipts and the project inventory JSON remain unchanged. This
does not check remote logs or apply one-off query overrides.

`gregale logs defaults show --app payments-api` targets the generated preview
command at that app without changing the preferred view or linked app. The
slug is validated locally; existence and access are not checked. JSON adds
`app` and `app_verified: false` only when an app is supplied. Missing preferences
and broken references still omit preview commands, while retaining the requested
target metadata. App completion uses the existing local cache for spaced and
inline `--app` values. All inspection remains offline and read-only.

## Resume the most recently recorded saved Job run

Use `gregale jobs resume --saved-job --latest` to observe the most recently
recorded local run for the Job in `.gregale/jobs.json`, on the current profile
and API. Add `--logs` to inspect failed-task logs, or `--results` to wait for
success and collect that exact run's artifacts. Saved task counts and result
preferences are not inherited by this selector; `--results` retains its existing
artifact-filter behavior.

New accepted-run records include `recorded_at`, a local UTC recording time.
Selection compares timestamps as instants, not UUIDs, file modification times,
or remote execution start times. Runs missing from local records are not
discovered. If any matching older record lacks a timestamp, or the newest
timestamp is tied, selection stops before API access and offers explicit UUID
choices. Existing records remain usable with `--run`; nothing is migrated or
resubmitted. Invalid records still fail full-store validation before filtering.

`--latest` requires `--saved-job` and cannot be combined with `--run`, `--list`,
`--overview`, `--forget`, or `--current-connection`. `--latest=false` keeps the
existing selection behavior. Human output identifies the selected UUID and
recording time. With `--json`, the action's stdout format is retained and a
`latest_recorded_run_selected` event containing the selected record is written
to stderr. The existing remote identity checks still apply before observation,
logs, or result collection. Selection does not submit or replay a run.

Preview that selection with `gregale jobs resume --saved-job --latest --dry-run`.
Add `--logs` or `--results` to preview those actions instead of waiting.
The report shows the selected UUID, recording time, configured connection,
action, applicable wait timing, and proposed results directory. No API calls,
waiting, downloads, or file writes occur. Remote status, identity, credentials,
destination safety, and artifact-filter rules are not verified by the preview.

The copyable `action_command` uses the exact Job and UUID, omitting automatic
selection and `--dry-run`, so a newly recorded run does not change its target.
It retains the current profile and relies on the API configuration/environment
when invoked; normal recovery still checks the recorded connection and identity.
JSON uses a single stdout report and emits no selected-run stderr event for
successful previews. Existing explicit `--run` (including prefixes), `--job`,
and unique-record selection can also be previewed. Legacy records remain
previewable by explicit UUID. Preview excludes record-maintenance and overview
modes; `--dry-run=false` retains the normal action. Ambiguous selections keep
their exact-run choice commands, including `--dry-run`, for offline review.

Workflow recovery supports the same offline review:
`gregale workflows resume-wait --run UUID --dry-run`, or add `--diagnose`
to preview reading diagnostics. `--workflow NAME` filters local records on the
current profile/API; repeated names across apps still require an exact UUID or
unique prefix. The report includes the recorded workflow, available app slug
and app ID, selected run, recording time when present, connection, action, and
applicable wait timing. App fields come only from the record, not linked context.

JSON contains a single preview report with `workflow`, optional `app`/`app_id`,
and the same `dry_run`, `selection`, `run_id`, `action`, `connection`, timing,
`action_command`, and `notes` fields as Job recovery previews. The command pins
the exact workflow name and UUID. Diagnostic previews omit wait timing.
Maintenance/overview options and timing flags with `--diagnose` remain invalid.
No authentication, API reads, waiting, or file writes occur, and remote identity
or diagnostics availability are not verified. Omit `--dry-run` for the normal
action; its existing server identity checks remain in place.

## Narrow recorded workflow runs by app

`gregale workflows resume-wait --workflow order-processing --app payments-api
--dry-run` filters local recovery records by both workflow and recorded app slug,
on the current profile and API. Omit `--dry-run` to wait, or add `--diagnose`
to inspect that exact run. If several runs still match, use a UUID or unique
prefix from the offered choices; no latest run is guessed. `--app` also works
alone, with `--run`, and with `--list` or `--overview`, but not `--forget`.

The slug must be valid and nonempty. It is compared with saved record metadata;
no linked app, remote app lookup, or app-ID-to-slug lookup is used. Older records
with only an app ID do not match a slug filter; use their explicit UUID without
`--app`. Empty filtered lists/overviews succeed, while unmatched recovery
selectors fail locally. Full-store validation still happens before filtering.
No defaults, linked targets, or records are changed by selection.

App, workflow-name, and UUID completion use validated local records for the
current connection. App completion can be narrowed by a completed workflow
selector; workflow and UUID completion respect the recorded-app filter.
Suggestions and exact-run preview commands preserve app selection. App metadata
is unverified until the normal read action performs its existing identity checks.

## Select the most recently recorded workflow run

`gregale workflows resume-wait --workflow order-processing --app payments-api
--latest --dry-run` selects the most recently recorded local run for that
workflow and recorded app on the current profile/API. Add `--diagnose` to preview
diagnostics, or omit `--dry-run` to observe the selected run. Both explicit
selectors are required unless `--saved-workflow` is used; linked defaults are
never substituted. UUID order, file modification
times, and remote run history are not used.

The same timestamp rules as Job recovery apply: every matching record must have
a valid `recorded_at`, and the newest timestamp must be unique. Matching older
records without timestamps or tied newest timestamps fail before API access,
with exact-run choices preserving the app, workflow, action, and preview flag.
Unrelated workflows, apps, and connections are excluded from selection after
full-store validation. Explicit UUID recovery remains available for older records.

`--latest` excludes `--run`, `--list`, `--overview`, `--forget`, and
`--current-connection`; `--latest=false` keeps normal selection. Actual actions
report the selected record on stderr, using the same
`latest_recorded_run_selected` JSON event as Job recovery. Successful previews
instead return one stdout report with `selection: "latest_recorded"` and an
exact-UUID action command. Existing remote identity checks still apply to real
waits and diagnostic reads. No run is submitted or replayed by selection.

## Reuse project-local workflow recovery selectors

Configure `gregale workflows defaults set order-processing --app payments-api`
once, then preview recovery with
`gregale workflows resume-wait --saved-workflow --latest --dry-run`.
`workflows defaults show` reports the workflow/app pair, preference path, and
copyable preview command; `workflows defaults clear` removes only the preference.
These commands are offline. Set validates selectors but does not require a
matching local record or verify remote existence. A preview still needs an
unambiguous matching record and, with `--latest`, valid unique recording times.

The versioned name/app pair is stored in a private, atomically written
`.gregale/workflows.json` at the nearest linked project root, otherwise Git root,
otherwise working directory. Nested directories inherit it. The file does not
store a connection, credentials, timing, or run settings. JSON is strict and
bounded to 8 KiB; symlinks and nonregular files are rejected. Missing preferences
show as not saved, and clear is idempotent; malformed regular files can be
explicitly cleared. Recovery records and linked app defaults are retained.

`--saved-workflow` is opt-in and excludes explicit `--workflow`, `--app`, and
`--forget`. It works with exact/prefix `--run`, `--latest`, `--dry-run`,
`--diagnose`, `--list`, and `--overview`; their existing option constraints still
apply. Both saved selectors filter records on the current profile/API. There is
no fallback to linked targets when preferences are missing or invalid, and
ordinary/explicit recovery does not read this file. `--saved-workflow=false`
keeps the existing explicit behavior. Empty lists/overviews succeed, while
unmatched saved selectors receive preference-specific guidance.

Preview JSON adds `defaults_path` when the saved selector is used. The offered
action command pins the exact workflow, app, and UUID rather than depending on
preferences at execution time. UUID completion reads the saved pair once and
filters validated local records on the current connection; conflicting app/name
completion stays empty, and context probes do not read preferences. The project
inventory and its `--problems-only` view include workflow recovery preferences,
validating the file format without claiming remote or recorded-run availability.

## Check saved workflow recovery readiness

`gregale workflows defaults show` now checks local accepted-run records for the
saved workflow/app on the current profile/API. It reports the matching count and
whether `--latest` can resolve a unique run. JSON adds `readiness` with status
`ready`, `no_matches`, `missing_timestamps`, `tied_timestamps`, or `unreadable`,
the record-store path, unverified connection details, and `latest_ready`.
Ready reports include `latest_run_id` and `latest_recorded_at`; legacy reports
include the count of matching records without timestamps.

The latest preview command is offered only for ready references. Missing matches
include a current-connection listing command. Missing or tied recording times
include a matching-record listing and a clearly labeled exact-UUID preview
template. Unreadable stores direct users to the local configuration problem
report, omit unknown counts and selected UUIDs, and return nonzero while keeping
the structured stdout report. Missing matches and timestamp limitations remain
advisory (exit zero); explicit UUID recovery may still be possible.

The complete store is validated before filtering, using pinned, regular-file
reads bounded to 8 KiB per record, 1000 JSON records, and 10000 directory entries.
Malformed, oversized, symlinked, or nonregular records and incomplete scans
cannot produce a ready result, even if they would be excluded by selection.
Raw record contents are not displayed on failure. Checks are offline and
read-only; remote identity, status, diagnostics, and access remain unknown.
Missing preferences still report not saved without scanning records. Set
receipts, clear behavior, and the project preference inventory stay unchanged.

## Preview changing workflow recovery defaults

`gregale workflows defaults set order-processing --app payments-api --dry-run`
shows the resolved preference path, current and proposed workflow/app pair, and
local recovery readiness for the proposed pair. No files are written or
directories created, and no API calls occur. Flags can precede or follow the
workflow name. JSON includes `dry_run: true`, `changed: false`, `would_change`,
`before_saved`, `before` (null when absent), `after`, `path`, and `readiness`.
Matching saved selectors report no change needed.

Readiness follows the same bounded full-store validation as defaults show.
Ready proposals include a copyable recovery preview; missing or tied timestamps
include listing and exact-UUID guidance. Every matching-record command uses the
explicit proposed workflow/app, without `--saved-workflow`, so it does not
depend on existing preferences. Missing matches and timestamp limitations are
advisory; unreadable records return nonzero with the structured preview report.
Existing malformed, oversized, symlinked, or nonregular preference files fail
before readiness inspection, without displaying their contents.

Destination write permissions and remote resources are not verified. Omit
`--dry-run` to save; `--dry-run=false` retains normal set behavior and its receipt.

## Open the saved Job overview directly

The overview also displays the effective profile and API endpoint, including
where each setting came from. This helps distinguish a profile's configured
endpoint from a `FAAS_API` override without another context command. JSON adds
`connection` with the same source fields and `address_redacted`. Credentials,
query parameters, and fragments are removed from displayed URLs; malformed
addresses are hidden. This is configuration context captured before existing
overview reads, with no separate account/permission check or extra requests.

The overview now guides the next action using the observed run state. Active
runs offer waiting or cancellation preview. Failed/dead-letter/cancelled runs
offer unsuccessful-task inspection, matching logs, and a replay preview.
Succeeded runs retain result collection. All actions pin the effective profile
and resolved run UUID, including sampled failure logs; image/configuration
commands pin the profile too. Preview commands include `--dry-run` and read
current eligibility when executed. Generating actions adds no API requests or
recovery submissions, and the overview receipt shape remains unchanged.

Run `gregale jobs` to open the existing saved-Job overview: readiness, latest
run state, and useful next commands. It behaves like `gregale jobs overview`,
including JSON output, and performs only the overview's authenticated read
requests. With no saved Job, it reports the defaults path and setup/explicit-Job
examples without API calls or writes. Invalid preference files retain their
errors. Use `gregale jobs --help` for command help or `jobs overview JOB` for an
explicit Job. No interactive flow or new overview format is introduced.

## Fix missing saved-Job settings

Across Jobs, Workflows, logs, and secrets, running `defaults` without a
subcommand uses the existing `show` behavior. These commands inspect local
settings without API requests or writes, preserving each command's saved-state
output, missing-settings behavior, and validation errors. For example, use
`gregale workflows defaults`, `gregale logs defaults`, or
`gregale secrets defaults`; add `--help` for command help.

Run `gregale jobs defaults` to inspect saved Job settings and recovery guidance.
It also shows how to save a Job when no settings exist, using the same local,
read-only behavior as `jobs defaults show`. Use `jobs defaults --help` for help.

Use Tab after `gregale jobs defaults set` or `gregale jobs defaults update` to
suggest locally recorded Job names on the current profile/API. Task counts and
artifact-filter values remain separate from the name position. Existing
`update --remove-include`/`--remove-exclude` saved-pattern completion still works.
Job-name suggestions require neither saved preferences nor API access and make
no writes; manually typed names are still supported. Update itself continues to
require valid existing preferences.

If a saved-Job shortcut has no preferences file, its error now says `No saved
Job`, shows the resolved defaults path, and offers two examples: supply a Job
to the current command, or save one with `jobs defaults set my-job`. Replace
`my-job` with your actual Job name. Examples pin the effective profile. Task
shortcuts retain the selected task index, and retry/replay examples use
`--dry-run` so they preview rather than submit recovery.

This covers runs, status, wait, results, tasks, attempts, logs, retry, and replay.
The error stays nonzero, JSON remains a stderr problem, and no example is run
automatically. Invalid or unreadable preference files keep their existing
errors. Explicit Job targets keep their behavior, and `jobs defaults show`
continues to report a successful empty saved state. No directories, preferences,
records, or API requests are created by this guidance.

## Keep collection recovery on the selected profile

Result collection already supplies commands to continue waiting or collect
outputs without submitting another run. Those commands now include the effective
`--profile`, including in JSON `resume_command` and `download_command` fields.
They retain the exact run UUID, destination, timeout/poll interval, artifact
filters, and existing resume behavior. Terminal-failure status commands and
incomplete-identity history commands pin the profile too.

The shared behavior covers `jobs results`, `jobs wait --out-dir`, `jobs run
--results`, retry/replay with results, and recorded recovery with results.
Collection requests, receipts, and exits remain unchanged, with no additional
API calls or local writes for guidance generation. API endpoint environment
overrides still apply when copied commands are run.

## Inspect unsuccessful retained attempts

Retained output/artifact inspection and next-page commands now include the
effective `--profile`, including when the original command selected a profile
explicitly. Unavailable bulk-log task read commands do the same in human output
and JSON summaries. Commands retain exact run/task/attempt identifiers, byte
limits, filters, and pagination. Copying them no longer falls back to another
configured profile. Generation adds no remote reads or writes, and endpoint
environment overrides still apply when the copied command is run.

Use `gregale jobs attempts --task 2 --unsuccessful-only --all` to inspect failed,
timed-out, out-of-memory, and cancelled retained outcomes for a task, using the
existing saved-Job/latest target behavior. Explicit Job/run/task targets also
work. `--all` controls scanning all pages from the starting offset; without it,
the filter applies only to the fetched page. Next-page commands retain the filter.

JSON retains the existing page receipt and pagination fields. Empty matches
return zero with a fetched-history explanation; active or expired attempts may
not appear. `--failed-only` still selects its original three states. Only one
filter may be enabled; false-valued flags remain inactive. This reads retained
history only and does not retry tasks, replay runs, or create files.

## Read logs for all unsuccessful Job tasks

You can now use `gregale jobs logs` or `gregale jobs logs nightly-report`
without a task index. With no explicit filter or task, the command shows current
logs for unsuccessful tasks. The omitted Job uses saved defaults and the omitted
run resolves the latest remote run once. Supplying `JOB RUN_UUID` uses that exact
run. Human output briefly explains the default; JSON keeps the existing bulk
log rows and summary without extra prose.

Explicit task selection (`--task N` or a positional index), retained-attempt
reads, and enabled filters retain their previous behavior. Explicit false-valued
filters opt out of the implicit default and still require a task selection.
Missing defaults, invalid targets/options, and unavailable logs retain existing
errors. This is read-only remote inspection with no prompts or task/run changes.

Use `gregale jobs logs nightly-report RUN_UUID --unsuccessful-only` to read
current logs for failed, timed-out, out-of-memory, and cancelled tasks, matching
the new unsuccessful task list. Saved-Job/latest target selection is also
supported. Task pages are validated before logs are read; individual unavailable
logs are reported with exact-task read commands while other tasks continue.
Cancelled tasks may have no logs if they never started or retention has expired.

The existing per-task byte limit, UTF-8-safe truncation, overall deadline, and
NDJSON log rows plus summary remain in use. Human output labels the summary as
unsuccessful tasks. `--failed-only` retains its original three states. Use only
one enabled bulk filter; explicit task indexes, `--task`, and retained `--attempt`
are incompatible. False-valued filters leave normal log reading unchanged.
This reads remote state only and creates no run records or files.

## List all unsuccessful Job tasks

Run `gregale jobs tasks nightly-report latest --unsuccessful-only`, or omit the
Job/run to use existing saved-Job/latest behavior, to scan failed, timed-out,
out-of-memory, and cancelled tasks across all pages from the starting offset.
These are recovery candidates; retry/replay previews still check current budget,
policy, and state. `--failed-only` keeps its existing three-state filter and
cannot be enabled together with the new filter.

Cancelled tasks now get the same bounded log/attempt/retry-preview guidance as
other unsuccessful tasks in human listings. Combine `--unsuccessful-only` with
`--artifacts-only` and `--include`/`--exclude` to inspect selected recorded outputs.
NDJSON remains task rows, empty matches return zero, and false-valued flags do not
enable filtering. Pagination validation still fails the whole scan before output;
the command performs only its existing read requests and writes no local files.

## Complete explicit Job recovery targets

`jobs tasks`, `jobs logs`, and `jobs attempts` also offer offline Job-name and
run-UUID completion for explicit targets. Use Tab after the Job name to choose
one of its locally recorded runs on the current connection. Task-index slots
and values of `--task`, `--attempt`, pagination, filter, and byte-limit options
are kept separate from targets. Existing saved-Job/latest behavior and manually
entered UUIDs remain supported. Completion reads no preferences or API and
does not inspect task status, fetch logs, or alter JSON output.

`jobs status`, `jobs wait`, and `jobs results` also complete explicit Job names
and their recorded run UUIDs offline. Use this to select an exact run for
inspection, following, or result collection without copying its ID. Destination,
filter, and timing options remain separate from target completion. Tab performs
no remote reads or collection, and candidates do not verify current status or
artifact availability. Saved-Job and moving `latest` behavior remain unchanged
when those targets are entered or omitted as supported by the command.

`jobs cancel` uses the same offline Job-name and exact-UUID completion. It never
suggests a moving `latest` selector or assumes that a recorded run remains active.
Use `gregale jobs cancel JOB RUN_UUID --dry-run` to check current remote state.
Cancellation still requires an explicit Job and exact UUID; completion does not
change target selection or submit a cancellation request.

Use Tab after `gregale jobs retry` or `gregale jobs replay-failed` for locally
recorded Job names, and after an explicit Job name for its recorded run UUIDs.
Suggestions use only the current profile/API and are sorted and deduplicated.
Interspersed `--dry-run`, wait/result options, filters, and retry `--task N` are
parsed as options; their values do not become Job selectors. Task-index slots
are not completed from run records. Suggestions work without saved defaults or
login and never use a remote cache. Missing, malformed, unsafe, or over-limit
stores produce no suggestions, with no partial candidates.

Completion only helps enter explicit targets; it does not change default/latest
selection, validate remote existence, or limit manually entered remote UUIDs.
No files or records are changed, and context-only shell probes read no local
preferences or records. Existing Bash/Zsh/Fish/PowerShell helper hooks supply
these candidates when the installed completion script supports local names.

## Preview Job cancellation

Queued/running `jobs runs` and `jobs status` output now offers the exact-run
cancellation preview beside `jobs wait`. This also works in history filtered
with `--status queued` or `--status running`. Commands pin the current profile
and displayed run UUID and include `--dry-run`, so they inspect without
cancelling. Guidance remains bounded to three failed/active groups with an
overflow count, uses already fetched data, and leaves JSON output unchanged.

Run `gregale jobs cancel nightly-report RUN_UUID --dry-run` with an exact UUID
to inspect a cancellation target before sending a request. The preview verifies
Job/run ownership, displays status and task counts, and explains that cancellation
stops pending work and requests termination of running tasks without undoing
effects they already produced. Queued/running sources offer a profile-pinned
command for that exact run. Terminal sources return a successful nothing-to-cancel
report without a cancellation command. All reports offer exact-run status
inspection; JSON includes `would_cancel` and observed counts.

This requires login and two read-only API requests. No cancellation, record,
directory, or result file is created. A preview is an observation, not a
reservation; the run can change before cancellation is submitted. Missing Job
or UUID arguments, unsafe targets, mismatched identities, unknown statuses,
invalid counts, and read failures return nonzero without a partial preview.
Use the generated cancellation command to act after review. `--dry-run=false`
retains ordinary cancellation behavior; there are no added prompts.

## Preview a single task retry

When a retry or replay preview is blocked, read its next-step guidance instead
of reconstructing another command. Budget/window-blocked retries offer retained
attempts for the selected task and a linked replay preview for all unsuccessful
inputs. The latter may also be blocked and retains effective wait/results/filter
options. Other task states offer exact-run status inspection. Replay blockers
offer source status, Job/image information, or all source task outcomes.

Human output labels these as inspection/preview commands; JSON exposes optional
`next_steps` entries containing `reason` and `command`. Commands pin the current
profile and source UUID wherever applicable, use no moving `latest` selector,
and perform no mutations. Guidance is generated from the existing observations
without extra requests. Eligible reports and non-preview actions are unchanged.

Human `gregale jobs tasks --failed-only` output now includes exact-run retry
preview commands beside log and retained-attempt commands. The first three
displayed failures get copyable commands with `--task N --dry-run`; a count
explains remaining failures. Commands pin the current profile and resolved UUID,
so a newer run cannot change the preview target. Guidance uses already fetched
tasks and sends no additional API requests. `--json` task rows are unchanged.

Use `gregale jobs retry --task 2 --dry-run` with saved Job defaults, or
`gregale jobs retry nightly-report latest --task 2 --dry-run`, to inspect one
task before retrying. The positional form with an exact run UUID also works.
The preview resolves the target once, reads task state, and shows attempts,
remaining retry budget, capped backoff, and any flexible start-window blocker.
The remaining budget counts attempts permitted by the current retry policy;
it does not guarantee eligibility when task state or the start window blocks it.

Add `--wait` or `--results` to preview follow behavior and effective artifact
filters. Eligible observations provide an exact-run retry command; blocked ones
return zero with `observed_eligible: false` and no command. Reads, identity,
pagination, or task lookup failures return nonzero without a partial preview.
This requires login and read-only API access. It sends no retry request and
creates no accepted-run record, result directory, or file. The server rechecks
image readiness, policy, current limits, and task state on execution. Retrying
may repeat input effects; waiting and collection assess the entire run.

## Preview a Job replay without starting it

Human `jobs runs` and `jobs status` output includes a replay-preview command
for failed or dead-letter runs beside task/log inspection guidance. It pins the
current profile and displayed run UUID, so it does not select a newer run later.
Failure-filtered history uses the same bounded guidance: up to three run groups,
with a count for the rest. Generating commands adds no reads or writes and does
not establish replay eligibility. JSON output remains unchanged; running the
`--dry-run` command reads current replay eligibility without creating a run.

Run `gregale jobs replay-failed nightly-report latest --dry-run` to inspect the
newest remote source run before replaying unsuccessful inputs. Omit the Job to
use saved Job defaults, or provide an exact source UUID. Add `--wait` or
`--results` and their normal options to preview the subsequent follow/collection
behavior. Effective result filters are included in the generated replay command.

The preview reads Job/run identities and all task pages, then reports counts,
source execution settings, and observed eligibility. A terminal source with
unsuccessful tasks and the same ready image digest offers an exact-UUID replay
command; otherwise the report lists blockers. No replay request, accepted-run
record, directory, or result file is created. Invalid or incomplete source data
fails without a partial report. Complete blocked previews return zero with
`observed_eligible: false` in JSON. The preview requires login and read-only API
calls; it is not a reservation, and the server checks current eligibility and
limits when replay is requested. Replayed inputs execute again and may repeat
effects. Results paths use `NEW_RUN_ID` as a clearly labeled placeholder.

## Complete workflow recovery preferences

Use Tab after `workflows defaults set` or `workflows defaults update` for local
workflow names, or after `--app` for recorded app slugs. Suggestions use only
validated workflow records on the current profile/API; linked targets and remote
app caches are not used. The other supplied selector narrows candidates. Update
uses the saved app when completing a new workflow name, and the saved workflow
when completing a new app unless an explicit replacement is already supplied.

Both `--app SLUG` and `--app=SLUG` support completion, including interspersed
options and `--dry-run`. Update requires valid saved preferences; set works
without them. Missing, malformed, unsafe, or over-limit record stores produce
no suggestions. Context-only shell probes do not read preferences or records.
Completion is offline and read-only; manually typed new selectors remain valid.

## Update one workflow recovery selector

Use `gregale workflows defaults update --app billing-api --dry-run` to change
only the saved app selector, or
`gregale workflows defaults update nightly-report --dry-run` to change only
the workflow name. The omitted selector is preserved. Both can be supplied
together, and at least one must be supplied. Existing valid preferences are
required; use `workflows defaults set NAME --app SLUG` for initial setup.

The offline preview shows before/after selectors and local recovery readiness
for the proposed pair. Generated recovery commands use explicit selectors, so
they work before saving. An unchanged pair reports `would_change: false`.
Remove `--dry-run` to save through the private atomic writer. Saving does not
read recorded runs or contact the API, and remote target existence/access are
not verified. Invalid or unsafe preference files are errors without replacement.

## Preview clearing workflow recovery preferences

Run `gregale workflows defaults clear --dry-run` to see which preference file
and workflow/app selectors would be removed. The preview explains that
`--saved-workflow` will require new preferences and provides a profile-pinned
listing command with explicit selectors, usable after clearing the preference.
Recorded runs and linked targets remain unchanged. The listing is guidance;
the preview does not read the record store or verify that matches exist.

Missing preferences return zero with nothing to clear. Existing preferences must
be valid, regular, nonsymlink files within 8 KiB; unreadable or malformed files
return an error without a partial preview. The command is offline and read-only,
supports `--json` with before/after and removal effects, and creates no directories.
Use `gregale workflows defaults clear` to perform the removal after review.

## Check saved Job recovery readiness

`gregale jobs defaults show` now reports local recovery readiness for the saved
Job name on the current profile/API, alongside saved task counts, result and
image-wait settings, artifact filters, and existing new-run guidance. JSON keeps
`defaults`, `path`, and `next_step`, and adds `recovery` with `status`, record-store
path, unverified connection details, matching-run count, and `latest_ready`.

Ready reports identify the latest recorded UUID/time and offer
`jobs resume --saved-job --latest --dry-run`. Missing matches show a
current-connection listing command. Missing or tied recording times show the
matching local list and a clearly labeled exact-UUID preview template. These
states are advisory and return zero. Unreadable or incompletely scanned stores
return nonzero with a structured report, omitting unknown counts and selected
UUIDs. Readiness applies to recovery of recorded runs, not admission of a new run.

The shared workflow/Job scanner validates the full store before filtering and
uses pinned regular-file reads bounded to 8 KiB each, 1000 JSON records, and
10000 directory entries. The check is offline and read-only; remote identity,
status, logs, artifacts, credentials, and access are not verified. New-run
guidance retains its existing behavior. Set/update receipts, preference
previews, clear behavior, and the project inventory remain unchanged. With no
saved Job defaults, `show` succeeds, displays the destination path and a setup
example, and skips recovery scanning. JSON returns `saved: false`, `path`,
`setup_command_template`, and `note`, without `defaults` or `recovery`. Replace
the example Job name and task count before saving. Invalid or unreadable Job
defaults still return an error.
