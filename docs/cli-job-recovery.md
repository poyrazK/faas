# Recover Job runs

When a Job run fails or its result is uncertain, recover it from the terminal: read the failed tasks' logs, preview a retry, replay or cancellation, and resume from a saved run record.

## Preview saved Job defaults offline

```bash
gregale job defaults set nightly-report --tasks 4 --results --dry-run
gregale job defaults update --tasks 3 --dry-run
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

### Unset the saved task count

Unset only the saved task count with
`gregale job defaults update --clear-tasks --dry-run`, then remove `--dry-run`
to save. The Job name, results preference, image-wait preference, and artifact
filters remain unchanged unless explicitly updated in the same command.
Unset means no saved task-count override; it does not force zero tasks on a run.
`--clear-tasks` is supported on `update` only and conflicts with any explicit
`--tasks` value. Explicit `--clear-tasks=false` does not clear the count and
requires another update operation. Already-unset counts preview as no change.

### Edit saved artifact filters

Edit saved artifact filters incrementally with repeatable `--add-include`,
`--remove-include`, `--add-exclude`, and `--remove-exclude` flags on
`jobs defaults update`. For example:

```bash
gregale job defaults update --add-include '*.csv' --dry-run
gregale job defaults update --remove-exclude 'private*' --dry-run
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

### Preview the next run from saved defaults

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

### Preview clearing saved defaults

Preview removal with `gregale job defaults clear --dry-run`. It shows the
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

### Rename a context preset

Rename a context preset offline with
`gregale config preset rename staging staging-eu`. It preserves the preset's
project/app/environment targets and all other presets without applying or
changing the active context. Unknown sources, invalid or identical names, and
existing destinations are rejected without changes. Malformed/unsafe stores
fail through the existing guarded reader. The store is re-read before the
private atomic write to detect intervening changes.

The receipt includes `from`, `to`, `preset`, absolute `path`,
`active_context_changed: false`, and a profile-preserving `inspect_command`
using the new name. Source-name tab completion is available; destination names
remain user-chosen. No API queries, credential changes, or prompts are involved.

### Copy a context preset

Create a similar preset without applying it first:

```bash
gregale config preset copy staging staging-eu
gregale config preset update staging-eu --env staging-eu --dry-run
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

### Preview deleting a context preset

Preview a preset deletion with
`gregale config preset delete staging --dry-run`. It displays the preset's
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

### Copy a saved log view

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

### Preview deleting a saved log view

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

## Explicitly run the saved Job

`jobs run --saved-job --dry-run` explicitly uses the Job name and omitted
settings from `jobs defaults`, just like the existing name-omitted invocation.
Explicit flags override saved settings; explicit inputs suppress the saved
task count. Supply `--tasks N` or inputs when neither is configured.
`--saved-job` cannot be combined with a positional Job name. Missing or invalid
defaults fail before API access. Remove `--dry-run` to submit through the
existing run path; preview never submits a run.

## Resume the most recently recorded saved Job run

Use `gregale job resume --saved-job --latest` to observe the most recently
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

Preview that selection with `gregale job resume --saved-job --latest --dry-run`.
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

### Preview workflow recovery

Workflow recovery supports the same offline review:
`gregale workflow recover --run UUID --dry-run`, or add `--diagnose`
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

## Open the saved Job overview directly

The overview also displays the effective profile and API endpoint, including
where each setting came from. This helps distinguish a profile's configured
endpoint from a `GREGALE_API_URL` override without another context command. JSON adds
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
run state, and useful next commands. It behaves like `gregale job overview`,
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
`gregale workflow defaults`, `gregale logs defaults`, or
`gregale secrets defaults`; add `--help` for command help.

Run `gregale job defaults` to inspect saved Job settings and recovery guidance.
It also shows how to save a Job when no settings exist, using the same local,
read-only behavior as `jobs defaults show`. Use `jobs defaults --help` for help.

Use Tab after `gregale job defaults set` or `gregale job defaults update` to
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

Use `gregale job attempts --task 2 --unsuccessful-only --all` to inspect failed,
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

You can now use `gregale job logs` or `gregale job logs nightly-report`
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

Use `gregale job logs nightly-report RUN_UUID --unsuccessful-only` to read
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

Run `gregale job tasks nightly-report latest --unsuccessful-only`, or omit the
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
Use `gregale job cancel JOB RUN_UUID --dry-run` to check current remote state.
Cancellation still requires an explicit Job and exact UUID; completion does not
change target selection or submit a cancellation request.

Use Tab after `gregale job retry` or `gregale job replay` for locally
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

Run `gregale job cancel nightly-report RUN_UUID --dry-run` with an exact UUID
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

Human `gregale job tasks --failed-only` output now includes exact-run retry
preview commands beside log and retained-attempt commands. The first three
displayed failures get copyable commands with `--task N --dry-run`; a count
explains remaining failures. Commands pin the current profile and resolved UUID,
so a newer run cannot change the preview target. Guidance uses already fetched
tasks and sends no additional API requests. `--json` task rows are unchanged.

Use `gregale job retry --task 2 --dry-run` with saved Job defaults, or
`gregale job retry nightly-report latest --task 2 --dry-run`, to inspect one
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

Run `gregale job replay nightly-report latest --dry-run` to inspect the
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

## Check saved Job recovery readiness

`gregale job defaults show` now reports local recovery readiness for the saved
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
