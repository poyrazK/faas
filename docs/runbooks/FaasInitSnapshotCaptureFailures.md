# FaasInitSnapshotCaptureFailures

## Symptom

Schedd has recorded at least three `snapshot_failed` or
`reuse_cleanup_failed` init snapshot path outcomes in 15 minutes, representing
more than 10% of attempts for either `site=prime` or `site=park`. The alert
waits five minutes before firing. `before_checkpoint_failed` is shown on the
dashboard but excluded from this platform warning: it means an opted-in app
callback rejected capture or timed out.

## Triage

Open the **Init snapshot path outcomes** and **Init snapshot capture p95**
panels on the faas fleet dashboard. Check whether failures affect prime,
later park, or both. On an affected schedd host:

```sh
curl -fsS http://127.0.0.1:9103/metrics \
  | grep -E '^schedd_init_snapshot_(attempt_total|capture_duration_seconds_(count|sum))'
journalctl -u faas-schedd --since '30 minutes ago' --no-pager \
  | grep -E 'snapshot capture timing|park: snapshot|capture warm snapshot failed'
```

| Outcome | Meaning | Next check |
| --- | --- | --- |
| `snapshot_failed` | A new terminal capture failed after entering the snapshot path | Check vmmd logs, snapshot storage capacity, upload errors, and `snapshot_ms` versus `budget_ms` in schedd logs. |
| `reuse_cleanup_failed` | A usable snapshot existed, but destroying the source guest failed | Check vmmd destroy errors and host guest cleanup. No new capture was attempted. |
| `before_checkpoint_failed` | The application callback rejected or timed out | Inspect that app's guest logs and callback deadline; this outcome does not trigger the platform alert. |
| `captured` | The vmmd capture call succeeded | If no snapshot row appears later, inspect `snapshot_written` delivery and imaged separately. |
| `reused` | An existing init snapshot was kept | No new capture or callback ran; exclude this from capture latency calculations. |

For a failed **prime**, inspect the deployment's `snapshot_prepare` failure
code. For a failed **later park**, use `gregale wake-timeline <slug> <wake-id>`
to find `wake.park_failed` and its closed reason, then inspect the same app's
logs if the reason is `before_checkpoint_failed`. The metric labels deliberately
contain no app, deployment, instance, or raw error text.

## Recover

Resolve the vmmd or storage failure shown by the logs. Re-run a failed
deployment prime through the normal deployment retry path; a later park will
be retried on a subsequent running instance. Confirm that the alert's 15-minute
failure count falls below three and that new `captured` or `reused` outcomes
increase. Do not treat a `captured` counter alone as proof that imaged has
published the snapshot row.
