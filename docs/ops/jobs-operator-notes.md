# Jobs: operator notes

Daemon, storage and upgrade detail behind the customer [jobs page](../jobs.md).
It moved here so that page can be published (#5709).

## Image materialization

A newly created or image-updated job is `pending` until imaged resolves the
reference and publishes its immutable ext4 rootfs; schedd does not dispatch a
task until that artifact is ready. `ram_mb` bounds are `api.JobMinRAMMB` and
`api.JobRAMMB` in `pkg/api/limits.go`.

Image pulls may use account-owned private-registry credentials configured with
`gregale job registry`; passwords are sealed at rest and never returned by
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
