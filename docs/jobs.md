# Jobs

Jobs are bounded run-to-completion workloads. Create a job from an OCI image
reference, then dispatch one or more tasks; each run retains task status and
logs. A newly-created or image-updated job is `pending` until imaged resolves
the reference and publishes its immutable ext4 rootfs. Tasks remain queued
until that artifact is `ready`; pull/build failures are exposed as
`image_materialization_status=failed` plus an actionable error. A terminal
image failure marks any queued tasks failed and settles their runs; new runs
are rejected until `image_ref` is updated and materialization succeeds.

Image pulls may use account-owned private-registry credentials configured with
`gregale jobs registry`; passwords are sealed at rest and never returned by
the API. Imaged retries bounded pull/build failures across restarts, and
schedd does not dispatch a task until the job's ext4 artifact is ready.
Each materialization attempt writes a unique `jobs/<job-id>__<attempt-id>.ext4`
object, then publishes it only if its worker still owns the live claim. A
losing attempt cannot overwrite or delete the winner; reconciliation removes
unpublished and superseded objects.

Jobs created before OCI image materialization may still refer directly to an
`apps/...ext4` artifact. During the upgrade, these rows briefly show
`image_materialization_status=verifying_legacy` and cannot dispatch. imaged
copies a readable legacy layer to the legacy job-owned `jobs/<job-id>.ext4` key
before setting `ready`, so app-layer garbage collection cannot remove a
running job's image. A missing artifact becomes `failed` with an explicit
error; if the store cannot answer or the copy fails, the job remains
non-dispatchable and the check retries. OCI/GCS storage compresses newly
published job rootfs objects; previously stored uncompressed objects remain
readable.

Cancelling a task while its job VM is still restoring an artifact also cancels
that in-flight boot. The stop waits for vmmd to finish cleanup; a late boot
result cannot publish a runnable VM after cancellation.

The first execution on a compute node can take longer while that artifact
fills its local cache. Job tasks use their own timeout and lease reaper during
this phase; the shorter HTTP-app cold-boot watchdog does not apply.

```bash
gregale jobs add nightly --image registry.example/nightly@sha256:DIGEST --timeout 900 --retries 2
gregale jobs run nightly --tasks 10 --parallelism 3
gregale jobs run nightly --tasks 10 --parallelism 3 --arg=--dataset --arg=2026-09
gregale jobs run nightly --parallelism 2 --input=shard-a=s3://my-bucket/a --input=shard-b=s3://my-bucket/b
gregale jobs run nightly --tasks 20 --flexible --eligible-at=2026-10-01T00:00:00Z --latest-start-at=2026-10-01T04:00:00Z
gregale jobs add nightly-export --image registry.example/exporter:v1 --schedule "0 3 * * *" --timezone Europe/Istanbul
gregale jobs update nightly-export --schedule "30 3 * * *"
gregale jobs update nightly-export --unschedule
gregale jobs runs nightly
gregale jobs tasks nightly RUN_ID
gregale jobs retry nightly RUN_ID 0
gregale jobs logs nightly RUN_ID 0 [--max-bytes N]
```

`--schedule` makes a job recurring: schedd creates one single-task run at
each matching cron boundary, using UTC unless `--timezone` names an IANA
timezone. A scheduled run uses the job's current image, command, environment,
and retry/resource settings when it fires. Pausing a job stops new scheduled
runs without cancelling existing runs. Editing the schedule resets its
occurrence cursor; missed boundaries are coalesced into one run rather than
replayed as a burst. `--unschedule` returns the job to batch-only operation.

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
`GREGALE_OUTPUT_MANIFEST_PATH` before exiting successfully:

```json
{"version":1,"artifacts":[{"name":"result","uri":"s3://my-results/shard-a.parquet","size_bytes":1234,"sha256":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}]}
```

Gregale validates the bounded manifest and retains it on the task record;
`GET /v1/jobs/{name}/runs/{id}/tasks` returns `output_manifest` for successful
tasks. Artifact bytes stay in the object store chosen by the image. The image
must upload them and provide the checksum before writing the manifest. An
invalid manifest causes the task attempt to fail, so it follows the configured
retry policy. For an `obj://` artifact, call
`GET /v1/jobs/{name}/runs/{id}/tasks/{index}/artifacts/{artifact}/download`.
Gregale verifies the current object's size and SHA-256, then returns a
five-minute signed URL. The caller needs job and storage read access. Check
the downloaded checksum too, because an object can change after verification.
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
1 MiB) to retrieve a larger tail when the response is truncated.
