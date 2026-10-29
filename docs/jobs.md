# Jobs

For multi-step work that waits for time or events between steps, use durable
workflows: [code-first](code-workflows.md) (ordinary async functions) or
[declarative](event-driven.md#durable-workflow-waits) (steps in
`gregale.yaml`).

Jobs are bounded run-to-completion workloads. Create a job from an OCI image
reference, then dispatch one or more tasks; each run retains task status and
logs. A newly created or image-updated job is `pending` until Gregale has
pulled and prepared its image. Tasks stay queued until the image is `ready`;
pull and build failures show as `image_materialization_status=failed` with an
actionable error. A terminal image failure marks any queued tasks failed and
settles their runs; new runs are rejected until `image_ref` is updated and the
image prepares successfully. Gregale retries bounded pull and build failures.

`ram_mb` is the memory each task's VM gets. Omit it (or send 0) for the
256 MB default; an explicit value must be at least 64 MB (the smallest guest
that boots) and at most your plan's Job limit (Hobby 512 MB, Pro 2048 MB,
Scale 4096 MB).

Image pulls may use account-owned private-registry credentials configured with
`gregale job registry`; passwords are sealed at rest and never returned by
the API.

Cancelling a task while its job VM is still starting also cancels that boot; a
late boot result cannot start a runnable VM after cancellation.

The first execution on a host can take longer while the image fills that
host's cache. Job tasks use their own timeout during this phase, not the
shorter app cold-boot limit.

```bash
gregale job create nightly --image registry.example/nightly@sha256:DIGEST --timeout 900 --retries 2
gregale job run nightly --tasks 10 --parallelism 3
gregale job run nightly --tasks 10 --parallelism 3 --arg=--dataset --arg=2026-09
gregale job run nightly --parallelism 2 --input=shard-a=s3://my-bucket/a --input=shard-b=s3://my-bucket/b
gregale job run nightly --parallelism 2 --input-manifest-uri=obj://APP_ID/BUCKET_ID/inputs.json --input-manifest-sha256=sha256:DIGEST
gregale job run nightly --tasks 20 --flexible --eligible-at=2026-10-01T00:00:00Z --latest-start-at=2026-10-01T04:00:00Z
gregale job create nightly-export --image registry.example/exporter:v1 --schedule "0 3 * * *" --timezone Europe/Istanbul
gregale job update nightly-export --schedule "30 3 * * *"
gregale job update nightly-export --unschedule
gregale job runs nightly
gregale job tasks nightly RUN_ID
gregale job attempts nightly RUN_ID 0
gregale job retry nightly RUN_ID 0
gregale job replay-failed nightly RUN_ID
gregale job artifact-url nightly RUN_ID 0 result
gregale job logs nightly RUN_ID 0 [--max-bytes N]
```

`--schedule` makes a job recurring: Gregale creates one single-task run at
each matching cron boundary, using UTC unless `--timezone` names an IANA
timezone. A scheduled run snapshots the job's image, command, environment,
retry/resource settings, and failure rules when it fires. Pausing a job stops
new scheduled runs without cancelling existing runs. Editing the schedule or
schedule policy starts a new schedule revision.

Use `--schedule-policy` to set the per-schedule overlap, first-start deadline,
and recovery behavior for recurring Jobs and deployment-command Crons. For
example:

```bash
gregale job create customer-sync --image registry.example/sync:v1 \
  --schedule '*/5 * * * *' --timezone UTC \
  --schedule-policy '{"version":1,"overlap":"skip","start_deadline_seconds":120,"missed_runs":"skip"}'
```

`overlap` is scoped to occurrences from this job. `allow` admits another run,
`skip` durably records the nominal time and skips it while an earlier scheduled
run is queued or running, and `replace` asks Gregale to stop the prior run
before admitting its replacement. A replacement is admitted only after the
prior VM stop is confirmed. `start_deadline_seconds` limits how late the first
task may start; the deadline no longer applies after any partition has started.
On scheduler recovery, `missed_runs=coalesce_latest` records older due times
as coalesced and considers the newest due time, while `skip` records stale due
times without creating runs. These decisions retain their policy snapshot and
reason and can be inspected with `gregale job occurrences <name>` or
`GET /v1/jobs/{name}/occurrences`. Command Crons expose the same occurrence
history with `gregale cron occurrences <id>` and
`GET /v1/crons/{id}/occurrences`. HTTP request Crons also accept schedule
policies and explicit failure rules. An HTTP handler can return a bounded
business outcome code in `X-Gregale-Outcome-Code`; Gregale applies only the
configured code mapping and does not infer business retry safety from HTTP
status. A missing completion receipt follows `uncertain_outcome`, and an
explicit retry uses the account plan's finite durable invocation retry budget.
Occurrence history includes the last `outcome_code` and `work_decision`.

For example, retry an explicitly reported transient result, stop on invalid
customer data, and hold a request whose completion receipt was lost:

```json
{
  "version": 1,
  "rules": [
    {"outcome_codes": ["upstream_unavailable"], "action": "retry"},
    {"outcome_codes": ["invalid_record"], "action": "fail_partition"}
  ],
  "unmatched_failure": "fail_partition",
  "uncertain_outcome": "hold"
}
```

Return the corresponding code from the scheduled HTTP handler:

```http
HTTP/1.1 200 OK
X-Gregale-Outcome-Code: upstream_unavailable
```

Configure these rules with `gregale cron create --failure-rules JSON` or
`gregale cron update --failure-rules JSON`. Exit-code matchers remain
specific to command Crons and Jobs.

Command Crons use one task per occurrence and select the app's live deployment
when the occurrence is admitted. Their result manifest can also carry a
structured `outcome_code`, which is evaluated with the same explicit failure
rules as a job task:

```bash
gregale cron create --app customer-api --schedule '*/5 * * * *' \
  --command /app/bin/synchronize --schedule-policy \
  '{"version":1,"overlap":"replace","start_deadline_seconds":120,"missed_runs":"coalesce_latest"}'
gregale cron occurrences CRON_ID
```

Cron overlap is scoped to that Cron. `replace` first requests cancellation of
the previous command task and waits for its worker to confirm teardown before
admitting the new occurrence. A queued task is cancelled immediately; a task
that is restoring or running remains active until its lease owner acknowledges
the stop. Exit-code failure rules apply to command Crons; their bounded retry
budget is per occurrence. Set the retry budget and classifier on the Cron:

```bash
gregale cron update CRON_ID --retry-max 3 --failure-rules \
  '{"version":1,"rules":[{"exit_codes":[65],"action":"fail_partition"}],"unmatched_failure":"retry","uncertain_outcome":"hold"}'
```

Retry classification can be explicit and run-scoped. Define exit-code
mappings or structured application outcome codes, plus the action for
unmatched confirmed failures:

```bash
gregale job update customer-sync --failure-rules \
  '{"version":1,"rules":[{"exit_codes":[65],"action":"fail_partition"},{"outcome_codes":["invalid_record"],"action":"fail_partition"},{"outcome_codes":["upstream_unavailable"],"action":"retry"}],"unmatched_failure":"retry","uncertain_outcome":"hold"}'
```

`retry` leaves the partition eligible for its remaining attempts;
`fail_partition` retains its outcome and excludes it from automatic retries.
When a Job partition classified `retry` has no attempt left, its final
attempt records `dead_letter` (reason `retry_budget_exhausted`) and the run's
dead-letter count rises. `gregale job retry` refuses a partition past its
retry budget; re-run its input with `gregale job replay-failed JOB RUN_ID`.
For a command Cron, the bounded retry budget is per occurrence; for a Job, it
is per partition. `gregale cron runs CRON_ID --run TASK_ID` shows the reported
outcome code and saved work decision.
Outcome codes use lowercase letters, digits, `_`, `-`, or `.` and are limited
to 64 bytes. A mapped outcome code is authoritative even if the command exits
zero, so the application can report a permanent record rejection or a
retryable upstream failure without overloading process exit codes. To report
one, write it in the result manifest before the job or command-Cron command
exits:

```bash
tmp="${GREGALE_OUTPUT_MANIFEST_PATH}.tmp"
printf '%s\n' '{"version":1,"artifacts":[],"outcome_code":"invalid_record"}' > "$tmp"
mv "$tmp" "$GREGALE_OUTPUT_MANIFEST_PATH"
```

An unmapped outcome code does not change a successful exit; on a failed exit,
the exit-code rule or `unmatched_failure` decides. HTTP status matchers are not
available on the scheduled-work execution path.
`uncertain_outcome=hold` preserves a missing completion receipt for
reconciliation; `retry` consumes the remaining bounded retry budget and may
repeat the command, so use it only when the workload is safe to execute again.
For Jobs, the uncertain decision is retained in that partition's attempt
history. For command Crons, it is retained on the occurrence task and an opted-in
retry stays within the same occurrence. A run may override its definition's
failure rules with `jobs run <name> --failure-rules JSON`. Gregale preserves the
stable input identity and confirmed task outcome, so `jobs replay-failed`
creates work only for unsuccessful partitions. Tasks still need idempotent
side effects because a lost completion receipt cannot establish whether the
external operation committed.

`--unschedule` returns the job to batch-only operation.

`--tasks` is the total queued batch size; it may exceed `--parallelism` and
the account live-job limit. Only claimed tasks create VMs. Each claim checks
the run parallelism and account live limit atomically across scheduler replicas;
remaining tasks stay queued until capacity opens.

`--arg` may be repeated to replace the job command's arguments for one run.
The executable stays the same. The API accepts `arguments: []` to remove all
trailing arguments. Each new run captures its effective command, image
reference and digest, artifact key, RAM, merged environment, retry budget,
and task timeout, so those values remain stable in run history. A run created
while its image is pending binds the matching artifact when it becomes ready.
A task receives `GREGALE_RUN_ID`, `GREGALE_TASK_INDEX` (zero-based),
`GREGALE_TASK_ATTEMPT` (one-based), and `GREGALE_TASK_COUNT` in its environment.
`GREGALE_PARTITION_INDEX` and `GREGALE_PARTITION_COUNT` expose the same stable
partition identity to images that use those names.
Gregale sets these values after job and run environment overrides are merged.
Run environment overrides must use valid customer variable names and fit the
account plan's variable count and value size limits; the `GREGALE_` prefix is
reserved for platform identity.

An ordered `inputs` array in `POST /v1/jobs/{name}/runs`, or repeated CLI
`--input ID=REF`, creates one task per entry. The stable zero-based task index
and unique `input_id` are retained on the task record across retries.
The run records manifest version `1` and a SHA-256 digest of the ordered input
array; numeric fan-out runs use version `0`.
`GREGALE_INPUT_ID` and `GREGALE_INPUT_REF` identify the assigned input inside
the guest. Gregale treats the reference as an opaque string; the image must
have its own access to the referenced data. Numeric `--tasks` runs remain
available when input binding is unnecessary.

For larger input sets, the runs API accepts `input_manifest_uri` and
`input_manifest_sha256` instead of an inline `inputs` array. The URI must be
an account-readable `obj://<app-id>/<bucket-id>/<key>` object containing the
same JSON array of input bindings. Gregale verifies the exact object bytes
against the supplied SHA-256 and enforces a 16 MiB manifest limit and the
plan's task limit. The run retains the source URI, source checksum, and the
canonical digest of its validated inputs. Entry order determines task index.
For the CLI digest, use `sha256:` followed by the lowercase hex SHA-256 of the
manifest file's exact bytes. `--input-manifest-uri` and
`--input-manifest-sha256` must be supplied together and cannot be combined
with `--tasks` or inline `--input` values.

`--flexible` accepts a start window of at most 24 hours. `--eligible-at`
defaults to the time the run is accepted; `--latest-start-at` is required.
Standard queued tasks have dispatch priority. Flexible tasks become eligible
within their window when capacity is available. Each compute node reserves
capacity for one 512 MB, one-vCPU app wake before admitting flexible work.
Gregale cancels each task
that has not started by the latest-start time and retains its task record with
an expiry message; running tasks keep their normal timeout. The latest-start
time is an admission limit, not a completion deadline. Flexible runs currently
use the same pricing as standard runs. Scheduler metrics record starts,
deferrals, expiry, and queue duration; usage minutes retain run ID and
execution class so billing can be reviewed before any rate change.

Runs use `continue` failure policy by default: other eligible tasks continue
after one task exhausts its retries. `--fail-fast` cancels tasks that have not
started once a task fails permanently; tasks already running may finish. The
run and task records retain each successful or failed input outcome.

For structured results, the task writes a JSON manifest to the path in
`GREGALE_OUTPUT_MANIFEST_PATH` before exiting:

```json
{"version":1,"artifacts":[{"name":"result","uri":"s3://my-results/shard-a.parquet","size_bytes":1234,"sha256":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}],"outcome_code":"processed"}
```

Gregale validates the bounded manifest and retains it on the task record;
`GET /v1/jobs/{name}/runs/{id}/tasks` returns `output_manifest` for successful
tasks. The optional `outcome_code` is retained for both successful and failed
attempts and is visible in task and attempt inspection. Failed tasks may report
an outcome code but cannot publish artifacts. Artifact bytes stay in the object
store chosen by the image. The image must upload them and provide the checksum
before writing the manifest. An invalid manifest causes the task attempt to
fail, so it follows the configured retry policy. For an `obj://` artifact, call
`GET /v1/jobs/{name}/runs/{id}/tasks/{index}/artifacts/{artifact}/download`.
Gregale verifies the current object's size and SHA-256, then returns a
five-minute signed URL. The caller needs job and storage read access. Check
the downloaded checksum too, because an object can change after verification.
`gregale job artifact-url` prints the signed URL and expected digest; add
the global JSON output option to retrieve the full signed request, including
headers and expiry.
Missing objects return 404; an object whose current bytes disagree with the
manifest returns 422.
External `s3://` and `gs://` artifacts remain customer-managed references.

Job definitions cannot be edited or deleted while a run has queued or claimed
tasks. This prevents customer edits from changing the image reference, command,
environment, resource limits, or retry policy mid-run. Cancel the run or wait
for it to finish before updating the job.

Keep tasks idempotent and write checkpoints outside the VM if a retry must
resume work. Set a timeout and retry budget that match the downstream service,
and use the job run id as the correlation id in application logs. Failed runs
remain inspectable; `jobs retry` re-queues one failed task while its retry
budget remains, preserving the run history and applying capped backoff.
`GET /v1/jobs/{name}/runs/{id}/tasks/{index}/attempts` lists the immutable
outcome of each completed attempt. To rerun failed, timed-out, OOM, or cancelled
inputs from a terminal run, call
`POST /v1/jobs/{name}/runs/{id}/replay-failed`. This creates a linked standard
run with a fresh attempt budget. Each replay task includes `source_task_index`
so its original input can be traced. Replay requires the current ready job
image to match the source image reference and resolved digest. A legacy run
without a captured digest cannot be replayed. Cancel a run
when its work is no longer useful.

A VM boot failure before your command starts also consumes one configured
retry. The next attempt waits for the same capped backoff; when retries are
exhausted, the task records an `infra` error explaining that the image
artifact or VM boot path needs attention. A task with `--retries 0` therefore
fails after its first unsuccessful boot instead of creating VMs indefinitely.
An expired task lease follows the same bounded retry policy and becomes a
dead-lettered timeout if no attempts remain; it is never counted as a
customer cancellation.

`jobs logs` returns a 64 KiB tail by default. Pass `--max-bytes N` (up to
1 MiB) to retrieve a larger tail when the response is truncated. Pass
`--follow` (`-f`) to stream a task's output while it runs: the stream waits
while the task is queued, follows each retry attempt in turn, and ends when
the task finishes (ADR-1163).
