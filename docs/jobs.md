# Jobs

Jobs are bounded run-to-completion workloads. Create a job from a digest-pinned
image, then dispatch one or more tasks; each run retains task status and logs.

```bash
gregale jobs add nightly --image registry.example/nightly@sha256:DIGEST --timeout 900 --retries 2
gregale jobs run nightly --tasks 10 --parallelism 3
gregale jobs runs nightly
gregale jobs logs nightly RUN_ID 0
```

Keep tasks idempotent and write checkpoints outside the VM if a retry must
resume work. Set a timeout and retry budget that match the downstream service,
and use the job run id as the correlation id in application logs. Failed runs
remain inspectable; cancel a run when its work is no longer useful.
