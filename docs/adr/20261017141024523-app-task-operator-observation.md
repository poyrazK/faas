# Release-command and app-task operator observation

Status: accepted

## Context

Snapshot-prime observation exposes release_blocked, but logs are the only runner
health signal. A release task can be queued, restoring, running, awaiting
cancellation or abandoned after lease expiry. Completed command verdicts cannot
measure outstanding work. Spec section 12 permits private operator diagnostics;
section 17 and Appendix D do not authorize changing task admission or execution.

## Decision

Add an optional read-only store aggregate over every nonterminal app task, by
three fixed kinds and nine fixed classes: queued, retry_wait, held,
deadline_overdue, restoring, running, cancellation_pending, lease_expired and
unknown. SQLC executes one grouped query; MemStore uses one lock. No list/page
limit, deployment status filter or owner filter applies. This is the shared
fleet claim queue, so overlapping replicas must never be summed. Deleted-app
active tasks remain visible until teardown or lease recovery.

Queued admission accounts for due retries, missed start deadlines and already
started occurrences, current exclusive operation/generation/lease/attempt
budget, and PostgreSQL cutover admission. MemStore has no modeled cutover fence
and retains its existing admission reader's no-fence semantics. Terminal task
verdicts do not contribute to backlog. Counts retain unknown-age subsets.

Known boundaries are max(created,updated,retry) for currently claimable tasks,
command start for running tasks, cancellation request, lease expiry and missed
start deadline. They are not continuous class-residence timers: no timestamp
records becoming eligible after an intentional hold. Restoring updated_at is
rewritten by renewal, so restore start cannot be reconstructed. Restoring,
retry-wait and held ages remain explicitly unknown, without a migration or
invented history. Nonfinite, missing, future or inconsistent lifecycle evidence
belongs to unknown. Future retry/deadline/lease-expiry timestamps remain valid
when the corresponding event has not occurred.

Register metrics even with dispatch disabled and start the read observer before
coordinator construction/recovery. Read immediately and every 30 seconds under a
three-second budget; freshness expires 120 seconds after read start. Scrapes
perform no I/O and release locks before emission. Failure withdraws counts and
ages; it never fabricates zero. Observer shutdown withdraws expectation.

Instrument actual coordinator calls with bounded operation/return counters,
local occupied/configured slots, runner expectation/running, last successful
claim (including empty) and sweep, reported sweep changes, and completion
acknowledgements by returned durable status. Separate customer command verdicts,
lease loss, parent cancellation and completion cancellation races from operation
errors. Use the call context for interrupted gRPC statuses even when they do not
wrap context errors. Command deadlines remain task verdicts; restore, destroy
and finalization budget failures remain operation errors. A queued policy retry is not a terminal outcome or a completed delivery.
No raw command, output, secret, ID, token, arbitrary status/code or error text is
used as a label. No store interfaces used for execution are widened.

Ship three recordings and six internal warnings, with a runbook and generated
operator catalogs. Require complete fresh evidence for workload/progress alarms.
Disabled dispatch suppresses runner/work warnings while retaining observation;
missing/inconsistent telemetry remains unknown. Busy slots need not claim work,
but sweeps must progress independently. Expected unknown restoring/held/retry
ages do not trigger evidence warnings. Running duration alone is not an outage.

## Consequences and adversarial review

The query covers the entire nonterminal backlog and must be monitored against
its three-second budget at fleet scale; it does not promise a bounded row scan.
Local worker occupancy cannot be subtracted from a fleet count. Metrics classify
observed gates but do not bypass them. Restore failures can include policy or
account refusals, so operation failures require bounded-log triage before
infrastructure attribution. Sweep counters report existing result fields only;
policy retries not represented by those fields are not inferred.

Review tested retry boundaries, renewal without fabricated restore timing,
exclusive expiry, cutover admission, expired leases with cancellation, future
and nonfinite evidence, full-backlog observation, disabled/stalled runners,
scrapes during blocked reads, cleanup-before-acknowledgement, customer verdicts
and real PostgreSQL transitions. PromQL scenarios cover thresholds/holds,
incomplete and stale evidence, busy pools and opt-out. Existing running fence,
VM calls, recovery/retry decisions and teardown order are preserved; no native
VM lifecycle acceptance is claimed or required for instrumentation alone.
