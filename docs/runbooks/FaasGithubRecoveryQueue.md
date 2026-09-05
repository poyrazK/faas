# FaasGithubRecoveryQueue

GitHub webhook deliveries and Check Run updates are durable queues owned by
`githubd`. A dead delivery can mean missing customer deployment work; a dead
Check update means the deployment continued but GitHub may show stale status.

## Symptom

The alert identifies either a dead webhook delivery, a dead Check Run update,
an actionable item that has stopped making progress, or a failure to collect
the recovery-queue metrics. Customers may see a missing deployment or a stale
GitHub status until the affected item is recovered.

## Verify

Run `gregalectl github status --status dead`. For a stalled queue, also inspect
`--status pending` and `--status processing`, then check `githubd` logs and apid
reachability. Do not copy webhook payloads into tickets; the operator view
intentionally omits them.

## Recover

After correcting the dependency failure, retry one dead item:

```text
gregalectl github retry-delivery --delivery-id <id> --yes
gregalectl github retry-check --deployment-id <id> --yes
```

The delivery retry is safe: downstream deployment and build IDs are stable for
the `(delivery_id, app_id)` pair. Confirm the item reaches `succeeded` and the
corresponding GitHub Check Run reflects the current deployment state.
