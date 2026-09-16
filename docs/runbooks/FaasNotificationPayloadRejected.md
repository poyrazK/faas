# FaasNotificationPayloadRejected

This warning means a platform daemon rejected an `app_changed` notification.
The source-of-truth database row remains intact, but the named consumer may
have missed cache invalidation, lifecycle reconciliation, or an SSE update.

## Symptom

The alert identifies the rejecting `consumer` and instance. Its logs contain
`bad app_changed payload`, and app policy or lifecycle changes may remain stale.

## Check

1. Inspect the named daemon's logs at the alert time.
2. Confirm every database trigger emits JSON containing `kind` and `app_id`.
   During a rolling upgrade, a bare canonical app UUID is also accepted.
3. Compare the running release on control-plane and compute nodes.

## Recover

1. Finish or roll back a mixed release.
2. Patch the affected app once after convergence.
3. Verify the rejection counter stops increasing on every daemon.

Resolve the alert only after the 10-minute increase is zero and a policy
change reaches all gateway processes.
