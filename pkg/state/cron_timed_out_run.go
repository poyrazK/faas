package state

// An HTTP cron run that timed out may still be running in the app: the
// gateway stops waiting at the request timeout, the handler does not.
// skip_if_running and overlap=skip treat such a run as active while the
// instance that served it is still running or draining, for at most
// api.CronTimedOutRunHold (prod CLI e2e 2026-11-15 #13). A run that never
// started (received_at null, e.g. a missed start deadline) does not count.

// timedOutCronRunReason is the skipped occurrence's reason.
const timedOutCronRunReason = "an earlier run of this cron timed out and may still be running"

// timedOutCronRunPredicate matches such an invocation aliased inv. Every
// query using it passes api.CronTimedOutRunHold as $2.
const timedOutCronRunPredicate = `(inv.outcome = 'timeout' and inv.received_at is not null
	and inv.completed_at > now() - $2::interval
	and (coalesce(inv.instance_id, '') = ''
	     or exists (select 1 from instances i
	                 where i.id::text = inv.instance_id
	                   and i.state in ('running','draining'))))`
