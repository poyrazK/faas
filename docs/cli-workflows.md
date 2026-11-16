# Workflows from the CLI

Start a workflow run and wait for it, read its history, and recover failed runs from the terminal.

## Guided workflow inspection and recovery

Run `gregale workflow status --interactive` to choose an app and workflow run,
then read its status, deployment, step states, attempt counts, and errors. The app
comes from `--app`, a saved link, or the existing app picker. Run selection is
paged and scoped to that app. `gregale menu` also opens this inspection flow.

Use `gregale workflow retry --interactive` to choose a failed or dead run
and a step that passes the server's manual HTTP retry preview. Review its
attempt and the skipped descendants that would reopen, then confirm (default:
no). The preview uses the captured workflow graph and current quota; it does
not reserve capacity. Retry can execute HTTP effects again. Older servers
without the retry preview offer no steps; explicit retry commands remain
available.

Use `gregale workflow cancel --interactive` to choose a pending, running,
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

## Using the linked app for workflows

Inside a checkout linked with an app, these commands use that app by default:

```sh
gregale workflow list
gregale workflow run nightly-report
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
gregale workflow run nightly-report --input-file input.json
gregale workflow run nightly-report --input-file -
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
gregale workflow wait RUN_ID --timeout 5m
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
gregale workflow run nightly-report --input-file input.json --wait --timeout 5m
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
gregale workflow status RUN_ID --check
gregale workflow status latest --workflow-name billing --check
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
gregale workflow steps latest --workflow-name billing --failed-only
```

`--failed-only` includes only steps whose recorded status is `failed`.
Explicit UUIDs also work. Human output includes commands for run diagnostics
and each failed step's attempts, using the selected UUID. These commands
inspect evidence; they do not retry or resume the workflow. When no steps
match, human output says so without claiming the run succeeded. JSON retains
NDJSON and emits no rows for an empty result. Step states may change after
the read. Without the flag, existing step output remains unchanged.

```sh
gregale workflow status latest --workflow-name nightly-report
gregale workflow steps latest --workflow-name nightly-report
gregale workflow diagnose latest --workflow-name nightly-report
gregale workflow attempts latest charge-card --workflow-name billing
gregale workflow resumes latest --workflow-name billing
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
For example: `gregale workflow attempts latest charge-card --workflow-name
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
gregale workflow wait latest --workflow-name billing --timeout 5m
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
gregale workflow list --workflow-name nightly-report --all
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
gregale workflow events RUN_ID approved --payload-file approval.json
gregale workflow events RUN_ID approved --payload-file -
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
gregale workflow resume RUN_ID --expected-resume-count 1 --wait --timeout 5m
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
gregale workflow retry RUN_ID STEP_NAME --wait --timeout 5m
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

## Narrow recorded workflow runs by app

`gregale workflow recover --workflow order-processing --app payments-api
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

`gregale workflow recover --workflow order-processing --app payments-api
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

Configure `gregale workflow defaults set order-processing --app payments-api`
once, then preview recovery with
`gregale workflow recover --saved-workflow --latest --dry-run`.
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

`gregale workflow defaults show` now checks local accepted-run records for the
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

`gregale workflow defaults set order-processing --app payments-api --dry-run`
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

Use `gregale workflow defaults update --app billing-api --dry-run` to change
only the saved app selector, or
`gregale workflow defaults update nightly-report --dry-run` to change only
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

Run `gregale workflow defaults clear --dry-run` to see which preference file
and workflow/app selectors would be removed. The preview explains that
`--saved-workflow` will require new preferences and provides a profile-pinned
listing command with explicit selectors, usable after clearing the preference.
Recorded runs and linked targets remain unchanged. The listing is guidance;
the preview does not read the record store or verify that matches exist.

Missing preferences return zero with nothing to clear. Existing preferences must
be valid, regular, nonsymlink files within 8 KiB; unreadable or malformed files
return an error without a partial preview. The command is offline and read-only,
supports `--json` with before/after and removal effects, and creates no directories.
Use `gregale workflow defaults clear` to perform the removal after review.
