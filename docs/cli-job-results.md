# Job run results and artifacts

After a Job run finishes, read its result, find the tasks that produced artifacts, and collect their outputs without copying run IDs.

## Next step after a successful run

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

## Find tasks that produced artifacts

To find tasks that produced artifacts without checking indexes individually:

```sh
gregale job tasks nightly-report latest --artifacts-only
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
gregale job tasks nightly-report latest --artifacts-only --include '*.csv'
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

## List artifact names

To discover artifact names before downloading:

```sh
gregale job artifacts nightly-report latest 0
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
gregale job artifacts nightly-report latest 0 --include '*.csv' --include 'summary.json'
gregale job artifacts nightly-report RUN_ID 0 --attempt 2 --include '*.csv'
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

## Inspect one attempt's manifest

To inspect the recorded manifest from a specific retained terminal attempt:

```sh
gregale job artifacts nightly-report latest 0 --attempt 2
```

The command pins the run, checks its identity, and searches retained attempt
pages for that exact task and attempt. Missing, active, or expired attempts
fail without falling back to current output. JSON keeps the same shape and
identifies the selected attempt. This mode lists metadata only and omits all
download commands: historical downloads are not supported by the server,
and `artifact-download` always reads the current task output.

## Collect results as part of a run

### Run or wait, then collect selected results

To run or continue a job while collecting only selected results:

```sh
gregale job run nightly-report --tasks 10 --out-dir ./results --include '*.csv'
gregale job wait nightly-report RUN_ID --out-dir ./results --include '*.csv' --include 'summary.json'
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

### Collect a run you already started

To finish waiting and collect an existing run without submitting another:

```sh
gregale job wait nightly-report RUN_ID --out-dir ./results
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

### Submit, wait and collect in one command

To submit once, wait, and collect results with one command:

```sh
gregale job run nightly-report --tasks 10 --out-dir ./results
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

## Choose which artifacts to download

### Exclude names

To omit unwanted artifact names:

```sh
gregale job artifact download nightly-report latest --all-tasks --include '*.csv' --exclude 'debug-*.csv' --out-dir ./results
gregale job run nightly-report --tasks 10 --out-dir ./results --exclude 'debug-*'
gregale job wait nightly-report RUN_ID --out-dir ./results --exclude 'debug-*'
gregale job tasks nightly-report latest --artifacts-only --exclude 'debug-*'
gregale job artifacts nightly-report RUN_ID 0 --attempt 2 --exclude 'debug-*'
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

### Include names

To collect only selected artifact names across tasks:

```sh
gregale job artifact download nightly-report latest --all-tasks --include '*.csv' --include 'summary.json' --out-dir ./results
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

## Download artifacts

### A whole run

To download current results across an entire run:

```sh
gregale job artifact download nightly-report latest --all-tasks --out-dir ./results
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

### Output directory

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

### Preview before writing files

To preview a bulk download before writing files:

```sh
gregale job artifact download nightly-report latest 0 --all --out-dir ./results --dry-run
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

### One task's artifacts

To download all current managed artifacts into a directory:

```sh
gregale job artifact download nightly-report latest 0 --all --out-dir ./results
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
invalid manifest fails. The overall deadline is five minutes.

### Resume a failed download

Retry individual failed artifacts into new files, or resume the bulk download:

```sh
gregale job artifact download nightly-report RUN_ID 0 --all --out-dir ./results --resume
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

### A single result

To download a managed job result directly:

```sh
gregale job artifact download nightly-report latest 0 report.csv --out report.csv
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

## Check a run

### Run summary

For a one-time run summary:

```sh
gregale job status nightly-report latest
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

### Success check for scripts

For a one-time success check in scripts:

```sh
gregale job status nightly-report RUN_ID --check
```

`--check` displays the same run summary and exits 0 only when its recorded
aggregate status is `succeeded`. Queued, running, failed, cancelled,
dead-letter, and unknown statuses exit 1 with an explanation on stderr.
JSON still emits the run object on stdout; a failed check emits the normal
JSON error on stderr. API errors retain their usual codes. The command reads
status once without polling, and ordinary status reads retain exit 0 when
the read succeeds. `latest` works too, but explicit UUIDs keep CI checks tied
to the intended run. A successful check describes the run, not app health.

## Inspect tasks and attempts

### Every task

To inspect a complete task list:

```sh
gregale job tasks nightly-report latest --all
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

### Every attempt of one task

To read all retained attempts for one task:

```sh
gregale job attempts nightly-report latest 0 --all
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

### Output from one attempt

To compare output from a particular retained attempt:

```sh
gregale job logs nightly-report latest 0 --attempt 1 --max-bytes 65536
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

## Find runs by outcome

To find runs with a particular outcome:

```sh
gregale job runs nightly-report --status failed --all
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

## Show only failed tasks

To focus on task failures without interactive browsing:

```sh
gregale job tasks nightly-report latest --failed-only
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

## Using `latest`

```sh
gregale job tasks nightly-report latest
gregale job logs nightly-report latest 0 --max-bytes 65536
gregale job attempts nightly-report latest 0
gregale job artifact get nightly-report latest 0 report.csv
gregale job wait nightly-report latest --timeout 10m
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

## Collect outputs from an exact saved Job run

`gregale job resume --results` selects the exact recorded run, verifies its
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
