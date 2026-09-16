# FaasNotificationPayloadRejected

This warning means a platform daemon rejected an `app_changed` notification.
The source-of-truth database row remains intact, but the named consumer may
have missed cache invalidation, lifecycle reconciliation, or an SSE update.

1. Check the `consumer`, `instance`, and job labels, then inspect that daemon's
   logs for `bad app_changed payload` at the alert time.
2. Confirm every database trigger emits JSON containing `kind` and `app_id`.
   During a rolling upgrade, a bare canonical app UUID is also accepted.
3. Compare the running release on control-plane and compute nodes. Finish or
   roll back a mixed release before testing policy propagation again.
4. Patch the affected app once after convergence and verify the rejection
   counter stops increasing on every daemon.

Resolve the alert only after the 10-minute increase is zero and a policy
change reaches all gateway processes.
