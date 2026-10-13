# ADR-1161 · Image runs (`gregale run --image`)

- **Status:** accepted for an operator-gated preview
- **Date:** 2026-10-13
- **Decision:** Add a `docker run`-shaped entry point that runs one command
  from any OCI image in a fresh microVM, without the caller first creating an
  app or a saved Job. An image run is an ordinary one-task Job run (ADR-099)
  against a platform-managed *runner job* of the new kind `image_run`. apid
  keeps one runner job per account and normalized image reference, so the
  image is pulled and materialized once and later runs of the same image
  start from the cached ext4 artifact. Nothing below apid changes: schedd,
  vmmd and guest-init already run Job tasks from a materialized image with a
  per-run command snapshot.
- **Why:** Gregale can already run arbitrary images to completion, but only
  through `gregale jobs add` + `jobs run` + `jobs logs`, a saved template
  that counts against the Job quota and lingers in `jobs list`. Container
  users expect `docker run alpine echo hi`: name an image, optionally a
  command, get its output and exit code. Disposable runs (ADR-171) cover
  caller-supplied source in sanitized runtime snapshots and app tasks
  (ADR-230) cover deployments; neither takes an image.

## Contract

`POST /v1/image-runs` (scope `deploy:write`, MFA, idempotent) accepts:

| field | meaning |
| --- | --- |
| `image` | OCI reference, required; normalized before use |
| `entrypoint` | optional argv replacing the image `ENTRYPOINT` |
| `args` | optional argv replacing the image `CMD` |
| `env` | per-run environment, validated like Job run `env_overrides` |
| `ram_mb`, `timeout_sec` | `ram_mb` 0 means the Job default; `timeout_sec` 0 means the plan's Job task timeout ceiling, since `docker run` has no deadline; both capped by the plan's Job ceilings |
| `pull` | `missing` (default) or `always` |

The command follows Docker's rules: the argv is the effective entrypoint
followed by the effective args, where the entrypoint is `entrypoint` or the
image `ENTRYPOINT`, and the args are `args`, else the image `CMD` unless
`entrypoint` was overridden. An empty argv is rejected with
`image_run_command_required`.

apid derives the runner job name `ir-<16 hex of sha256(normalized image)>`
and, under the account row lock, gets or creates that `image_run` job
(empty command, one task, no retries, parallelism 1) and bumps its
`updated_at`. The response is one of:

- `202 {status: "pulling", job, image_status}` while the image is not ready.
  A runner job whose materialization failed, or whose ready artifact predates
  this ADR's image-config columns, is re-queued by the same call. `pull:
  always` re-queues a ready image so a moved tag is pulled again.
- `201 {status: "queued", job, run_id, image_digest, command}` once the image
  is ready: apid creates a one-task run whose command snapshot is the
  resolved argv, with `retry_max = 0`.

Callers poll `GET /v1/jobs/{job}` for image readiness and call again to
start the run; runs, tasks, logs, cancellation and artifacts use the
existing `/v1/jobs/{job}/runs/...` routes unchanged.

## Data and ownership

- `jobs.kind` gains `image_run`. A CHECK keeps image-run jobs unscheduled.
  `jobs_account_name_uniq` already ignores deleted rows, so an evicted name
  can be recreated.
- `jobs.image_entrypoint` and `jobs.image_cmd` (nullable `text[]`, ≤ 64
  entries) record the image's own `ENTRYPOINT` and `CMD`. imaged writes them
  in the same fenced publication that marks the artifact `ready`, before the
  existing "use the job command when the image has none" fallback. NULL means
  the artifact was published before this ADR.
- apid stays the only writer of customer intent: it creates, touches,
  re-queues and evicts runner jobs. imaged only writes the materialization
  columns it already owns.
- Runner jobs are not customer templates. `GET /v1/jobs`, the Job quota
  count, schedules and customer operations exclude them, and `PATCH` returns
  `409 image_run_managed`. `GET` and `DELETE` by name work, so a caller can
  inspect a runner job or drop a cached image.

## Limits

`api.ImageRunCachedImages` caps live runner jobs per account (Free 0,
Hobby 3, Pro 10, Scale 25). Image runs follow `JobsAllowed`, so Free accounts
get the existing `402 jobs_not_allowed`. Creating a runner job at the cap
soft-deletes the least recently used runner job that has no non-terminal
run; imaged's deleted-job sweep removes its artifact. When every cached image
is in use the call fails with `409 image_run_cache_full`, carrying the limit
and observed count. Task RAM, timeout and concurrency use the Job limits and
the tenant RAM admission path; billing is the Job task's plan RAM + 8 MB per
running second.

## Rollout

The route is mounted only when apid runs with `FAAS_IMAGE_RUNS=1`; otherwise
it returns 404. Enable it after the migration and the imaged that writes the
image-config columns are deployed. Artifacts published by an older imaged are
re-queued on first use, so the rollout order cannot produce a wrong command.

## Consequences

- `gregale run --image REF [ARGS...]` creates the run, waits for the image,
  waits for the task, prints its stored output and exits with the task's exit
  code. Ctrl-C cancels the run.
- Output is the Job task's stored log tail, printed when the task finishes;
  there is no live stream or stdin. `gregale run -it --image` needs an attach
  path for Job tasks (the ADR-1037 transport targets app tasks) and is a
  separate decision.
- A moved tag keeps running the cached digest until `--pull always` or
  eviction, as `docker run` does with a local image.
- Private registries work through the existing per-job registry credential
  routes on the runner job; a first-class flag is a follow-up.

## Rejected alternatives

- **CLI-only composition over the Jobs API:** each run would create a
  visible template that counts against the Job quota, and an interrupted
  client would leave it behind.
- **One ephemeral job per run:** every run would pull and rebuild the image.
- **A new workload class with its own scheduler path:** Jobs already provide
  admission, materialization, metering, cancellation and logs; a second
  run-to-completion path would duplicate them.
- **Extending disposable runs (ADR-171):** their sanitized, networkless
  runtime snapshot is a security property, and images cannot be restored
  from a shared runtime snapshot.
