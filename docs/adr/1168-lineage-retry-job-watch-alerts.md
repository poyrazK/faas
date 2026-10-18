# ADR-1168: Lineage retry-job watch alerts

Status: Accepted
Date: 2026-10-13
Related: ADR-1167

## Decision

Add three opt-in conditions to the existing delivery-health watcher: classified
stalled pending job count, active retention-risk job count, and sustained nonzero
paused retry backlog. Use the existing count and duration bounds, lifecycle state
machine, fresh monotonic component timestamps, stable incident/sequence IDs,
durable checkpoint outbox and acknowledgement handling. Zero disables a threshold.

Read counts from execution.lineage_retry_jobs at execution.observed_at. Validate
its coverage, count completeness, nonnegative bounded partitions and warning
subsets before consuming a value. Treat missing, truncated or malformed job
observations as unknown, including a positive truncated lower bound; never use
that evidence to open or resolve a retry-job alert. Routing/execution identity
binding and the existing max-age checks still apply. Execution-root truncation and
handler outcome history do not independently invalidate this separate job block.

Paused duration measures consecutive local fresh observations of any nonzero
paused job backlog, with at least two reads. It is not the age of one paused job;
job membership can change during that interval. Lost/stale evidence, nonmonotonic
samples and read gaps reset confirmation and local duration according to the
existing lifecycle. A complete fresh zero resolves a confirmed condition.

Checkpoint metric validation accepts the new names and thresholds. Resume uses
the same scope/window/selection/threshold guards. Count incidents reconfirm from
fresh complete evidence under their saved incident identity; paused duration must
be established anew before reconfirmation. A reconfirmed existing incident is not
opened again. Pending alert sequences remain deliverable until acknowledged;
existing delivery semantics permit an unacknowledged notification to be replayed.
Unknown evidence emits confirmation_lost rather than resolved, and later complete
evidence may start a new confirmation lifecycle. Do not promise exactly-once
external notifications or silently clear incomplete evidence.

Expose CLI watch flags and Go watcher options, preserving old selections and
checkpoints when the new thresholds are omitted. Older CLI/SDK versions reject
checkpoints containing unknown new metric names; upgrade them before resuming.
All alert notifications remain local callbacks/stdout/stderr; no outbound notifier,
automatic resume, retry or cancellation is introduced. No wire API, SQL, database
migration, compliance registry or Node/Python generation change is required.
