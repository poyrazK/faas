# Environment diagnostics and durable Run completion

Status: implemented; nine-case functional internal-node qualification passed.
Preview writers remain gated and native production eligibility remains open.

Build workers append bounded, lease-fenced diagnostic events to a separate per-environment retained-tail record. The original environment table's column layout stays unchanged for N-1 readers that use SELECT *. Retirement erases the diagnostic record in the same transaction as its deletion acknowledgement. Each event has a durable sequence, stage, attempt and timestamp. Readers preserve account/key-family ownership and explicitly report retention gaps. Logs exclude boot output, credentials, URL parameters and host paths. Live transport failure falls back to the bounded completed-build tail; diagnostics never change VM lifecycle ownership.

Terminal source Run transitions atomically enqueue `execution.finished` in the existing account webhook delivery ledger. Every terminal path, including queued cancellation, expiry and recovery, uses the same database producer. The run ID identifies the event; the existing source-event/receiver unique index fences duplicate fanout. Webhook retries and redrive never resubmit execution. The payload contains receipt metadata and a receipt URL, without source, input, results, logs or artifact bytes. Receivers verify signatures and deduplicate stable event IDs; network delivery is at least once.

Account administrators configure existing account receivers and inspect/redrive their existing delivery history. Runs-only keys retain their receipt boundaries and cannot configure account receivers. Compatible webhook readers/dispatcher and migrations precede accepting the new event filter. Disabling new Run writers does not remove pending delivery obligations. No production rollout or native release qualification is implied.

Diagnostics use JSON cursor pages rather than a new streaming protocol. CLI and
SDK watchers poll those pages and retain the last sequence; evicted events produce
an explicit gap. A worker may append at most 512 live output events per build;
storage retains 128 events of at most 1024 UTF-8 bytes each. Writes have a two-second
deadline, and final-tail collection shares a bounded cleanup deadline. Lost log
transport produces a diagnostic and falls back to the completed-build tail.
The builder log RPC grants its certificate access only to builder instances.
Streaming authorization checks the decoded request, since admission with a nil
request cannot establish instance ownership. Customer execution guests remain
networkless.

The completion producer is a database trigger on terminal status transitions.
It inserts directly into the existing durable delivery ledger within the Run
transaction, following the account-level agent completion pattern. A delivery
insertion failure rolls back the terminal transition. The producer's unique
source-event/receiver key suppresses repeated fanout; external receivers still
need idempotent effects because HTTP acknowledgement can be lost. The existing
dispatcher owns signatures, retry policy, secret rotation, dead-letter retention
and redrive. No second execution queue or per-Run callback URL is introduced.

Apply the additive diagnostics schema and expanded receiver constraint before
deploying new readers and workers. Validate the constraint, complete its contract
migrations, and upgrade vmmd, builders, imaging, API readers and dispatchers before
accepting the new event filter. Subscription admission uses the existing
`FAAS_EXECUTION_API_ENABLED` and `FAAS_EXECUTION_V2_ENABLED` gates. Environment
build writers retain `FAAS_EXECUTION_ENVIRONMENTS_ENABLED`. Rollback disables new
writers and keeps compatible receipt/diagnostic readers, delivery dispatchers and
GC until admitted work and delivery obligations settle. The schema is retained.

The [2026-11-15 evidence](../ops/evidence/20261115-run-diagnostics-completion/README.md)
records live diagnostics and durable cursor replay, eight signed completion
deliveries across an actual scheduler restart, all nine real-VM cases and clean
physical leakchecks. Memory/PostgreSQL contracts, signed HTTP retry/redrive,
API/CLI/SDK checks and final Linux metal lint pass. The report distinguishes the
compiled VM snapshot from the final failure-path and migration-identity
refinements. The Google-virtualized host provides functional evidence;
production installer and non-virtualized native release gates remain separate.
