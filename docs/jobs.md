# Jobs

Jobs are bounded run-to-completion workloads. Create a job from an OCI image
reference, then dispatch one or more tasks; each run retains task status and
logs. A newly-created or image-updated job is `pending` until imaged resolves
the reference and publishes its immutable ext4 rootfs. Tasks remain queued
until that artifact is `ready`; pull/build failures are exposed as
`image_materialization_status=failed` plus an actionable error.

Image pulls may use account-owned private-registry credentials configured with
`gregale jobs registry`; passwords are sealed at rest and never returned by
the API. Materialization is retry-safe across imaged workers, and schedd does
not dispatch a task until the job's ext4 artifact is ready.

Jobs created before OCI image materialization may still refer directly to an
`apps/...ext4` artifact. During the upgrade, these rows briefly show
`image_materialization_status=verifying_legacy` and cannot dispatch. imaged
checks the canonical artifact store before returning a present artifact to
`ready`. A missing artifact becomes `failed` with an explicit error; if the
store cannot answer, the job remains non-dispatchable and the check retries.

The first execution on a compute node can take longer while that artifact
fills its local cache. Job tasks use their own timeout and lease reaper during
this phase; the shorter HTTP-app cold-boot watchdog does not apply.

```bash
gregale jobs add nightly --image registry.example/nightly@sha256:DIGEST --timeout 900 --retries 2
gregale jobs run nightly --tasks 10 --parallelism 3
gregale jobs runs nightly
gregale jobs retry nightly RUN_ID 0
gregale jobs logs nightly RUN_ID 0 [--max-bytes N]
```

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

`jobs logs` returns a 64 KiB tail by default. Pass `--max-bytes N` (up to
1 MiB) to retrieve a larger tail when the response is truncated.
