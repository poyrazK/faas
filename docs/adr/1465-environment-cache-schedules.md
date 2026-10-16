# ADR-1465: Durable one-time environment cache schedules

Status: Accepted

## Context

Management agents need to review cache changes before a maintenance window.
Existing apid workers own delayed customer intent; schedd owns VM lifecycle.
MCP sessions and bearer tokens are unsuitable as execution lifetimes.

## Decision

Store one immutable owner/app/project/environment action and timestamp in a new
apid-owned table. Exactly one guarded policy replacement/inheritance restore or
explicit exact-path/whole-host purge is accepted. Schedule UUID is also the
native action retry UUID. Compare the complete request before recovering an
existing schedule, before time/quota/revision admission checks. Lock account,
app and environment in existing intent order. Bound future time to 30 days and
active schedules to 100 per app/environment in api/limits.go. Retain terminal
records as idempotency receipts. Physical owner deletion cascades schedules.

Apid claims due records using FOR UPDATE SKIP LOCKED, a random lease token and
60-second lease. Claim is a durable transition to executing. Only scheduled
records can be cancelled; cancellation and claim serialize on the same row.
Normal apid background workers process bounded batches of ten with 20-second
per-action contexts. No worker writes instance state. Native policy/purge writes
recheck eligibility and immutable IDs; policy expected_revision is enforced at
execution. Existing atomic action receipts prevent duplicate revision/generation
on retry after commit/reply loss. Recovery checks existing exact action receipts
before account/revision rejection, so a committed action remains executed even if
eligibility or desired policy changed after its reply was lost. Terminal
completion is fenced by lease token.
Transient failures remain executing for lease-expiry recovery; known rejections
are failed. No scheduled source-copy or historical-rollback actions in this slice.

Scheduling is explicit durable delegation. Token expiry/revocation, client exit
and local consent changes affect new MCP operations, not previously authorized
intent. Explicit cancellation is the revocation mechanism before claim; account
eligibility remains checked at execution. Exact environment write consent gates
apply/cancel; read consent gates review/status. Completed cached reviews still
recheck current consent. Persist review attempts/receipts in existing encrypted
plan journals. Reviews expire after 15 minutes, independently of execution time.

Schedule acceptance, native action execution and sampled gateway convergence
are three distinct facts. Return action receipts and separate current evidence;
never equate executed with converged. Superseded policy receipts stay executed
while status reports supersession. Purges reuse current-session generation
acknowledgements. Do not disclose gateway addresses or unrelated environment data.

## Consequences

At-or-after timestamps permit outage catch-up; this is not real-time scheduling.
Executing actions cannot be cancelled or reversed. Terminal records accumulate
until owner deletion; retention/compaction is separate work. Operators must run
normal apid workers and install migration 20261016153051002. Updated serving
gateways and prior cache migrations remain necessary for convergence evidence.
