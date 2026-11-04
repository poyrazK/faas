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

Sign in with `gregalectl auth login`, then run
`gregalectl github status --status dead`. For a stalled queue, also inspect
`--status pending` and `--status processing`, then check `githubd` logs and apid
reachability. Do not copy webhook payloads into tickets; the operator view
intentionally omits them. The command uses the authenticated operator API and
does not need database credentials.

A delivery whose `last_error` starts with `githubd: unprocessable webhook
delivery` is dead on its first attempt: githubd could not decode or route the
payload, and retrying the same bytes cannot succeed. That is a githubd bug or
an App subscribed to a payload shape githubd does not know. Retry it only after
the release that fixes the decoder is deployed. Pull request actions previews
do not use (labeled, edited, ...) are completed as ignored, not dead.

## Recover

After correcting the dependency failure, retry one dead item:

```text
gregalectl auth step-up
gregalectl github retry-delivery --delivery-id <id> --reason incident_123 --yes
gregalectl github retry-check --deployment-id <id> --reason incident_123 --yes
```

The delivery retry is safe: downstream deployment and build IDs are stable for
the `(delivery_id, app_id)` pair. Confirm the item reaches `succeeded` and the
corresponding GitHub Check Run reflects the current deployment state. Each
retry is idempotent and emits a trace ID plus a durable operator audit event;
use `gregalectl audit trace --trace-id <id>` for incident correlation.
