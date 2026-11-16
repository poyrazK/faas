# Jobs from the CLI

Run Jobs from the terminal: submit a run and wait for it, follow its progress, and save project-local defaults so everyday commands need fewer arguments.

## Follow a Job run

```sh
gregale job wait --interactive
gregale job wait my-job RUN_ID --timeout 10m
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

## Wait for a Job image to become ready

```sh
gregale job get my-job --wait-ready --timeout 5m
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

## Submitting and waiting for a Job run

To retry one task and wait for its run:

```sh
gregale job retry nightly-report RUN_ID 0 --wait --wait-timeout 10m
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
gregale job replay nightly-report SOURCE_RUN_ID --wait --wait-timeout 10m
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
gregale job run nightly-report --tasks 10 --wait --wait-timeout 10m
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
gregale job results nightly-report
gregale job results nightly-report latest --out-dir ./results
gregale job results nightly-report latest --out-dir ./results --include '*.csv' --exclude 'debug-*'
gregale job results nightly-report latest --out-dir ./results --dry-run
```

`jobs results JOB` defaults to `latest`. Without `--out-dir`, it saves under `./results/JOB/RUN_ID/`, keeping different runs separate. Explicit run IDs and destinations remain available; an explicitly empty destination is rejected. The destination is displayed before collection and included as `output_directory` in JSON collection/whole-run receipts. Preview reports it without creating directories.

`jobs results` resolves `latest` once, waits for that exact run to succeed, then collects managed artifacts into `task-N/` directories. It verifies matching existing files and skips them, without overwriting conflicting files or submitting a run. Missing output directories are created only when collection needs them. Failed runs do not start automatic collection. Recovery commands retain the run UUID, destination, profile, and filters.

`--dry-run` previews the artifacts currently recorded, including local conflicts and files that would be downloaded. It does not wait, request download URLs, create directories, or write files; an active run may record more outputs later. `--timeout` (default 10m) and `--poll-interval` (default 2s) control waiting; collection uses its existing separate five-minute deadline. Exclusions take precedence over includes. JSON uses the existing combined wait/collection receipt, or the whole-run preview receipt for `--dry-run`.

### Start a Job and collect its results

```bash
gregale job run nightly-report --tasks 1 --results
gregale job run nightly-report --tasks 1 --results --include '*.csv' --exclude 'debug-*'
gregale job run nightly-report --tasks 1 --results --out-dir ./exports
```

`--results` implies waiting and artifact collection. Without `--out-dir`, the destination is `./results/JOB/RUN_ID/`, using the ID returned by the single submission. It never looks up `latest` or submits again while waiting or collecting. Task count or input bindings remain required as for ordinary `jobs run`. Explicit destinations, filters, `--wait-timeout`, and `--poll-interval` remain available. Matching files in a fresh run's destination are treated as conflicts; recovery commands use `--resume` to verify existing files. Failed runs do not start collection. Human output shows the destination; JSON includes `output_directory` and recovery commands, including when waiting times out. Missing directories are created only if collection needs them; preview existing runs with `jobs results JOB --dry-run`.

### Save project-local Job defaults

```bash
gregale job defaults set nightly-report --tasks 1 --results
gregale job defaults show
gregale job run
gregale job results
gregale job run --tasks 3 --results=false
gregale job defaults clear
```

Defaults are stored in `.gregale/jobs.json` at the linked project root, otherwise the Git root, otherwise the current directory. Commands in subdirectories of a linked or Git project use the same defaults. Only the Job name, task count, results and image-wait preferences, and artifact filters are stored; no credentials or environment values. `set` replaces the entire defaults record; omitted task count is unset and omitted `--results` is false. These commands are local and do not require login or modify a remote Job. `clear` is safe to repeat.

`jobs run` fills an omitted Job name, task count, and results preference. Run options apply only to the configured Job, including when its name is explicitly supplied. An explicitly different Job does not inherit task or results defaults. Explicit flags override saved values; input bindings/manifests suppress the saved task count. Use `--results=false` to disable automatic collection. `jobs results` uses the saved Job name when no positionals are provided and saved artifact filters for that Job, and otherwise retains its latest-run and destination defaults. Human output identifies applied defaults and their file. Invalid defaults stop commands before submission; `show` helps inspect them and `clear` removes them. Existing commands work without a defaults file.

### Remember the outputs you normally collect

```bash
gregale job defaults set nightly-report --tasks 1 --results --include '*.csv' --exclude 'debug-*'
gregale job run
gregale job results
gregale job results --include '*.json'
gregale job results --all-artifacts
gregale job run --all-artifacts
```

Saved repeatable `--include`/`--exclude` patterns apply to `jobs results` and to `jobs run` when collection is enabled, only for the configured Job. They also apply when that Job name is explicitly supplied. Any explicit include or exclude replaces **both** saved filter lists, rather than adding to them. `--all-artifacts` ignores saved filters for one invocation; combining it with explicit filters is rejected. It does not enable collection on its own or change saved defaults. Exclusions still take precedence. Effective filters appear in collection/preview JSON and recovery commands, so recovery does not depend on defaults staying unchanged. `defaults show` displays saved filters; `defaults set` replaces the record and omitting filters clears them. Invalid patterns are rejected before saving or making API requests. Existing defaults files without filters remain valid.

### Preview a Job run before submitting

```bash
gregale job run --dry-run
gregale job run nightly-report --tasks 3 --results --dry-run
gregale job run --dry-run --json
```

`jobs run --dry-run` applies local Job defaults and explicit overrides, validates arguments, and shows the effective task selection, run overrides, waiting, results destination, and artifact filters. It requires no login and makes no API requests or file writes. The default destination contains `<run-id>` because no run has been submitted. An explicit destination is inspected without creating it. This preview does not fetch remote Job defaults or readiness, read input manifests, or check account limits. Remote-derived values are marked unknown rather than guessed; input bindings determine the task count when present. Environment values, argument values, and input references are redacted in human and JSON output. JSON contains a sanitized `request_preview`, collection options, and a `dry_run` marker; it is not a submission receipt. Remove `--dry-run` to submit with those options.

### Change one saved Job setting

```bash
gregale job defaults update --tasks 3
gregale job defaults update --results=false
gregale job defaults update --include '*.json'
gregale job defaults update --all-artifacts
```

`defaults update` changes an existing defaults record and preserves omitted settings. A positive `--tasks` replaces the saved task count; `--results=false` disables default collection. Supplying either filter flag replaces both saved include/exclude lists. `--all-artifacts` clears both lists and cannot be combined with explicit filters. An optional Job name changes the selected Job while retaining other saved settings. At least one change must be supplied; missing or invalid defaults are reported without creating or replacing a record. Writes use the same atomic replacement and private file permissions as `defaults set`. The command is local, requires no login, and returns the complete updated record and path with `--json`. Use `defaults set` to replace everything or `defaults clear` to remove the record.

### Check the latest Job status with fewer arguments

```bash
gregale job status
gregale job status nightly-report
gregale job status --check --json
gregale job status nightly-report RUN_ID --check
```

Without arguments, `jobs status` reads the saved Job name from project-local defaults. With a Job name but no run reference, it selects `latest`. Latest selection happens once; the summary and follow-up commands refer to that exact UUID. Explicit Job/run targets do not read local defaults. Missing or invalid defaults are reported before API requests when no Job is supplied. Status remains read-only and preserves the existing run JSON shape. `--check` exits successfully only for a recorded `succeeded` run, without waiting or submitting anything.

### Inspect tasks without repeating the Job and run

```bash
gregale job tasks
gregale job tasks --failed-only
gregale job tasks --artifacts-only
gregale job tasks nightly-report --all
gregale job tasks nightly-report RUN_ID --failed-only
```

Without positionals, `jobs tasks` uses the saved Job name. An omitted run reference defaults to `latest`, resolved once; pagination, log, and artifact follow-up commands use that exact UUID. Explicit Job/run targets do not read local defaults. Existing pagination, failed-task filtering, artifact-name filters, JSON task records, and interactive behavior remain available. Task inspection uses only the saved Job name, not the saved task count, results preference, or collection filters; pass artifact filters explicitly with `--artifacts-only`. Missing or invalid defaults are reported before API requests when no Job is supplied. This command remains read-only and does not download artifacts or create files.

### Read task logs with a shorter command

```bash
gregale job logs --task 0
gregale job logs nightly-report --task 0
gregale job logs nightly-report RUN_ID --task 0 --max-bytes 4096
gregale job logs --task 0 --attempt 2
```

With `--task`, the Job name defaults to the saved project-local Job and the run reference defaults to `latest`, resolved once. The task index remains explicit and must be nonnegative. `--attempt` and `--max-bytes` retain their current behavior. The original `jobs logs JOB RUN_ID|latest TASK_INDEX` syntax and JSON log response remain available. Explicit Job names bypass local defaults. Duplicate task flags or mixing `--task` with the positional task index are rejected before API requests. This is read-only; it does not guess a task, retry, download artifacts, or create files. Human output identifies the defaults file when it supplies the Job name.

### Inspect saved Job history

```bash
gregale job runs
gregale job runs --failed-only
gregale job runs --failed-only --all
gregale job runs nightly-report --status succeeded
```

Omitting the Job name uses the project-local saved Job; an explicit name bypasses local defaults. Existing limits, offsets, all-page traversal, and JSON run records retain their behavior. `--failed-only` filters fetched history to `failed` and `dead_letter` runs and cannot be combined with `--status`; cancelled runs are excluded. Filters apply to the fetched pages, so use `--all` to scan subsequent pages. Human follow-up commands retain the selected Job and history filter. This command is read-only and does not resolve `latest`, inspect task logs, submit runs, or collect outputs. Missing or invalid defaults stop an omitted-name command before API requests.

### Inspect the saved Job and check image readiness

```bash
gregale job get
gregale job get --check
gregale job get --wait-ready
gregale job get nightly-report --wait-ready --timeout 5m
```

`jobs info` and its readiness options use the saved Job when no name is supplied; explicit names bypass local defaults. `--check` reads once and exits 0 only when the recorded Job state is `active` and its image preparation status is `ready`. Paused Jobs, pending or failed images, and unknown states fail the check. It preserves the normal Job JSON response on stdout and reports failures on stderr. This check does not assess account limits, input accessibility, or guarantee execution success. `--check` and `--wait-ready` are mutually exclusive. Image waiting retains its existing behavior: it waits for the selected image, regardless of the Job's active/paused state, and pins Job/image identity. Wait timeout and polling flags require `--wait-ready`. No option submits a run or changes the Job.

### Wait for image preparation before starting a Job

```bash
gregale job run --wait-ready --results
gregale job run nightly-report --tasks 1 --wait-ready --image-wait-timeout 5m
gregale job run --wait-ready --dry-run
```

`--wait-ready` reads the selected Job and waits for its image to become ready before making the single run submission. Job/account/image identity and any resolved digest are pinned during polling. Image failure, unknown preparation state, changed identity, read failure, cancellation, or deadline stops before submission. `--image-wait-timeout` (default 5m) and `--image-poll-interval` (default 2s) must be positive and require `--wait-ready`. The image deadline includes the initial read and is separate from post-acceptance waiting and artifact collection.

Defaults, explicit overrides, filters, and `--results` continue to apply. `--dry-run` previews the image-wait settings locally without reading the Job or submitting. Pre-submission JSON failures have `phase: "image_wait"`, `submitted: false`, an exit code, and an inspection command; successful submissions retain existing receipts. Timeout exits 124; cancellation exits 130. Image readiness alone does not check active state, account limits, or input accessibility. The server validates acceptance; the submission API does not atomically pin the image against a concurrent update after polling. Uncertain submission errors still direct users to history before resubmitting.

### Remember image waiting for a Job

```bash
gregale job defaults update --wait-ready
gregale job run
gregale job run --wait-ready=false
gregale job defaults update --wait-ready=false
gregale job defaults set nightly-report --tasks 1 --results --wait-ready
```

Saved `--wait-ready` applies to runs of the configured Job when that flag is omitted. Explicit `--wait-ready=false` disables it for one run; other Jobs do not inherit it. `defaults show` and `run --dry-run` display the preference. `defaults update` preserves it when omitted, and `defaults set` resets it to false when omitted. Existing files without `wait_ready` remain valid and default to false. Image timing options can be supplied when saved image waiting is enabled; they are still per-invocation options and must be positive. Disabling waiting while supplying image timing flags is rejected. Status, task, log, info, and results commands do not inherit this run preference. The existing image preparation timeout, cancellation, and single-submission behavior remain unchanged.

### See Job readiness and the latest run together

```bash
gregale job overview
gregale job overview nightly-report
gregale job overview --json
```

`jobs overview` uses the saved Job when its name is omitted and otherwise bypasses local defaults. It shows Job state, image preparation, and the latest run's status and task counts. The latest history selection is read once; details and follow-up commands use that exact UUID. Jobs with no recorded runs still show readiness and configuration guidance. If the latest run has failures or is failed/dead-letter, overview scans failed task metadata and offers log commands for up to three tasks, plus a command to inspect all failed tasks. It does not read log content or artifact manifests/download URLs. Succeeded runs offer results collection; queued/running runs offer waiting. Non-ready images offer image-wait guidance.

Overview makes only read requests, has a 30-second overall deadline, and never starts runs, waits for completion, downloads outputs, or writes files. Read/identity failures return an error instead of presenting an incomplete summary or falling back to an older run. The JSON summary contains only selected Job/run status fields and action commands, excluding environment snapshots and storage references. Counts are recorded snapshots and may change while the command runs. Generated commands retain the current profile; results commands continue to use matching local collection defaults when executed.

### Understand the overview at a glance

`jobs overview` now adds separate readiness and latest-run assessments in human output and JSON (`readiness_assessment`, `run_assessment`). It describes paused/active Job state independently from ready/preparing/failed image state, so a succeeded previous run does not hide current preparation problems. Run assessments distinguish queued, in-progress, succeeded, failed, dead-letter, cancelled, and unknown states; no history reports “No runs recorded yet.” Active-run progress counts succeeded, failed, and cancelled tasks as finished, excludes overlapping dead-letter counts, and omits the fraction when recorded counts are inconsistent or the total is unknown. These are recorded snapshots, not execution guarantees. Assessments use already-fetched metadata and add no API requests. Failed or unknown image states offer inspection instead of waiting for a known failure.

### Use the overview as a health check

```bash
gregale job overview --check
gregale job overview nightly-report --check --json
```

`--check` prints the normal overview and exits 0 only when the recorded Job state is active, image preparation is ready, and the latest run succeeded. Paused/unknown Job states, non-ready images, other run outcomes, and no recorded runs fail. All failed conditions are reported together on stderr. JSON adds `check: {passed, reasons}` to the overview only when the flag is enabled; the reasons array is empty when it passes. Human output marks a successful check. Without `--check`, overview retains its existing output and exit behavior. The check uses already-fetched metadata, adds no requests or mutations, and assesses recorded state rather than guaranteeing a future run will succeed. Read/identity errors remain errors and do not produce a partial health check.

### Correct common run argument errors

Run validation errors include correction guidance for missing saved Jobs or task selection, filters without collection, empty destinations, timing options without waiting, conflicting artifact filters, input-manifest combinations, malformed input bindings, and negative retry counts. Run examples use `--dry-run` to let you inspect corrected settings before submitting. Examples are illustrative: choose the task count and input values your Job needs, and retain your other options. Guidance never reprints supplied environment values, argument values, or input references. With `--json`, corrections remain in the stderr problem detail and stdout stays empty for validation failures. Validation still stops before API requests or file writes. Inspect `jobs defaults show` before updating a saved task count, especially when running a different explicit Job.

### Wait for the saved Job's latest run

```bash
gregale job wait
gregale job wait nightly-report
gregale job wait --timeout 5m
gregale job wait --out-dir ./results --include '*.csv'
```

Without positionals, `jobs wait` uses the saved Job name. An omitted run reference defaults to `latest`, resolved once, then all polling and recovery commands use that exact UUID. Explicit Job names bypass local defaults. Existing timeout/polling flags, exit codes, JSON receipts, explicit run IDs, and `--out-dir` collection with explicit filters retain their behavior. Only the saved Job name is used: run task counts, image waiting, results preferences, and saved collection filters do not apply to `jobs wait`. Waiting never submits a run. Missing or invalid defaults stop omitted-name commands before API requests. Existing interactive behavior is unchanged; interactive collection remains unsupported.

### Recover from a failed readiness check

Failed `jobs info --check` and `jobs overview --check` now include suggested next commands on stderr. Preparing images offer `jobs info JOB --wait-ready`; failed/unknown images and paused/unknown Job states offer inspection. Overview additionally offers exact-run waiting for active runs, failed-task inspection and available task log commands for failed/dead-letter runs, task inspection for cancelled/unknown outcomes, or a local first-run preview when there is no history. Duplicate commands are omitted. JSON stderr problem details add `suggested_commands`, an array of `{purpose, command}`; existing stdout Job/overview receipts and check exit codes remain intact. Commands preserve the current profile and exact run UUID. Suggestions use already-fetched metadata and add no requests. Nothing is run automatically; first-run guidance uses `--dry-run` and never submits a Job.

### Read all failed-task logs in one command

```bash
gregale job logs --failed-only
gregale job logs nightly-report --failed-only --max-bytes 4096
gregale job logs nightly-report RUN_ID --failed-only
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
gregale job retry --task 2
gregale job retry --task 2 --wait
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
gregale job retry --task 2 --results
gregale job retry nightly-report RUN_ID 2 --results --include '*.csv'
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
gregale job replay
gregale job replay --wait
gregale job replay nightly-report latest --wait
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
gregale job replay --results
gregale job replay nightly-report RUN_ID --results --include '*.csv'
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
gregale job attempts --task 2 --failed-only --all
gregale job attempts nightly-report --task 2
gregale job attempts nightly-report RUN_ID --task 2 --limit 20
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
gregale job artifacts --task 2
gregale job artifacts --task 2 --attempt 1
gregale job artifacts nightly-report RUN_ID --task 2 --include '*.csv'
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
gregale job artifact download --task 2 report.csv --out report.csv
gregale job artifact download nightly-report --task 2 report.csv --out report.csv
gregale job artifact download nightly-report RUN_ID --task 2 report.csv --out report.csv
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
gregale job artifact download --task 2 --out ./saved.csv -- -report.csv
```
