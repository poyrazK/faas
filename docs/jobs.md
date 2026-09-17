# Jobs

Jobs are bounded run-to-completion workloads. Create a job from a digest-pinned
image, then dispatch one or more tasks; each run retains task status and logs.

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

`jobs logs` returns a 64 KiB tail by default. Pass `--max-bytes N` (up to
1 MiB) to retrieve a larger tail when the response is truncated.
