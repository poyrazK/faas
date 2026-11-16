# Interactive Job commands

When a Job command runs in an interactive terminal without its required arguments, the CLI prompts for them. This page lists what each guided flow asks.

## Inspect Job task logs interactively

```sh
gregale job logs --interactive
# Bound the returned log payload:
gregale job logs --interactive --max-bytes 65536
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
gregale job retry --interactive
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

## Start a Job run interactively

```sh
gregale job run --interactive
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
gregale job cancel --interactive
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
gregale job update --interactive
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
gregale job create --interactive
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

## Inspect retained Job attempts interactively

```sh
gregale job attempts --interactive
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
gregale job artifact get --interactive
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
gregale job replay --interactive
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
gregale job occurrences --interactive
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
gregale job registry set --interactive
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
gregale job registry rm --interactive
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
gregale job next
gregale job next --json
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
gregale job tasks --interactive
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
gregale job delete --interactive
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
