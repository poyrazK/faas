# FaasJobInstanceCleanupFailed

This alert means schedd found a live `kind='job_task'` instance without a
matching `job_tasks` row in `status='claimed'`, then vmmd failed to destroy it.
The five-second job reaper retries cleanup idempotently, but the instance can
consume host capacity and accrue metered RAM until a retry succeeds.

## Triage

1. Inspect `schedd_job_instance_reconcile_total` by `outcome`. A rising
   `found` with matching `cleaned` means automatic repair is working; a rising
   `error` means cleanup is still failing.
2. Check schedd logs for `reconcile orphaned job instance: destroy` and record
   the instance and node IDs.
3. Verify vmmd is healthy on that compute node and that the instance's
   Firecracker process, jail directory, and network namespace still exist.
4. Compare the `instances` row with `job_tasks.instance_id`. A terminal task or
   missing owner is eligible for cleanup; do not destroy a VM owned by a
   currently claimed task.

## Recovery

Restore vmmd reachability on the recorded node. The next reaper sweep should
destroy the VM, release the scheduler ledger reservation, and move the instance
row to `stopped`. Confirm the `cleaned` counter increases and the process,
namespace, and jail are gone. If vmmd cannot recover, stop the verified orphan
through the host runbook, then restart schedd so its in-memory admission ledger
is rebuilt from durable live rows.
