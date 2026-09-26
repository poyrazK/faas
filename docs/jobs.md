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
gregale jobs add nightly-export --image registry.example/exporter:v1 --schedule "0 3 * * *" --timezone Europe/Istanbul
gregale jobs update nightly-export --schedule "30 3 * * *"
gregale jobs update nightly-export --unschedule
gregale jobs runs nightly
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

Job definitions cannot be edited or deleted while a run has queued or claimed
tasks. This prevents customer edits from changing the image reference, command,
environment, resource limits, or retry policy mid-run. Cancel the run or wait
for it to finish before updating the job.

Keep tasks idempotent and write checkpoints outside the VM if a retry must
resume work. Set a timeout and retry budget that match the downstream service,
and use the job run id as the correlation id in application logs. Failed runs
remain inspectable; `jobs retry` re-queues one failed task while its retry
budget remains, preserving the run history and applying capped backoff. Cancel
a run when its work is no longer useful.

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
