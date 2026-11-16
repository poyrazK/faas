# Scheduled tasks, alerts and issues

Day-to-day operations from the terminal: scheduled HTTP tasks, alert presets and deliveries, and triaging issues.

## Create a scheduled HTTP task

```sh
gregale cron create --app my-api --interactive
# Use the linked app, or choose one:
gregale cron create --interactive
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
`gregale cron create --app my-api --schedule '0 * * * *' --path /tasks/hourly --timezone UTC`.

## Edit a scheduled HTTP task

```sh
gregale cron update --app my-api --interactive
# Use the linked app, or choose one:
gregale cron update --interactive
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
`gregale cron update ID` with explicit update flags.

## Browse scheduled-task history

```sh
gregale cron runs --app my-api --interactive
# Use the linked app, or choose one:
gregale cron runs --interactive
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
`--interactive`; for scripts or JSON, use `gregale cron runs ID` with
`--before`, `--limit`, or `--run` as appropriate. The guide reads history without
firing or canceling tasks.

## View upcoming scheduled tasks

```sh
gregale cron next --app my-api
gregale cron next --app my-api --json
# Use the linked app, or choose one in a terminal:
gregale cron next
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
gregale cron run --app my-api --interactive
gregale cron run --app my-api --interactive --timeout 5m
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
`gregale cron run ID`, then `gregale cron run get REQUEST_ID`. The `--app`
and `--timeout` flags apply only to interactive mode.

## Resume following a manual task request

```sh
gregale cron run get REQUEST_ID --wait --timeout 5m
gregale cron run get REQUEST_ID --wait --json
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
gregale alert preset enable --app my-api --interactive
# Use the linked app, or choose one:
gregale alert preset enable --interactive
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
gregale alert deliveries --app my-api --interactive
# Use the linked app, or pick an app when none is linked:
gregale alert deliveries --interactive
```

The flow shows the selected rule, asks whether to include test deliveries, and
lets you choose how many recent deliveries to fetch (1–100, default 20).
The table shows delivery status, attempts, HTTP response code, observed value,
and errors. When test deliveries are included, a `TEST` column identifies them.
This flow only reads delivery history; it does not send a webhook.

A reusable command is printed with the selected rule ID and active profile.
For scripts, use the explicit command with JSON output:

```sh
gregale alert deliveries ALERT_ID --app my-api --limit 20 --json
```

## Edit alert settings interactively

```sh
gregale alert update --app my-api --interactive
# Use the linked app, or choose one:
gregale alert update --interactive
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

## Inspect issues interactively

```sh
gregale debug issue get --app my-api --interactive
# Use the linked app, or choose one:
gregale debug issue get --interactive
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
gregale debug issue resolve --app my-api --interactive
# Use the linked app, or choose one:
gregale debug issue resolve --interactive
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
gregale debug issue ignore --app my-api --interactive
# Use the linked app, or choose one:
gregale debug issue ignore --interactive
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
gregale debug issue reopen --app my-api --interactive
# Use the linked app, or choose one:
gregale debug issue reopen --interactive
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
gregale debug issue assign --app my-api --interactive
# Use the linked app, or choose one:
gregale debug issue assign --interactive
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
