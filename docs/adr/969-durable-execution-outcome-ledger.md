# ADR-969: Durable execution outcome windows

Status: Accepted

## Context

Execution health previously depended on retained receipts, invocations and attempt history. Deleting source records removed observations and could permanently disqualify execution SLOs, including unaffected consumers. Handler replays could also replace the current successful execution used for latency sampling.

## Decision

Routing admission transactionally binds the actual root invocation incarnation to its subscription, acceptance timestamp and receipt identity. This association is authoritative; arbitrary invocation metadata cannot claim subscription ownership. A root key includes invocation creation time. Outcome keys additionally include the child invocation creation time, replay generation, attempt and outcome kind, so reused execution IDs cannot overwrite prior evidence.

Invocation transitions append immutable claim outcomes and successful completion latency. Claim capture does not depend on attempt-history rows. Unknown lease outcomes and deletion of an active claim remain unknown. These records describe recorded execution outcomes, not proof of external application effects. PostgreSQL triggers and the memory store use the same attribution and transition rules.

Outcome evidence is retained for 30 days independently of source retention. The scheduler prunes bounded batches using the defaults and maximum in `pkg/api/limits.go`. Root metadata is retained while outcomes, live executions, receipts or unfinished recovery jobs can still need its lineage, then becomes eligible for bounded cleanup. The ledger stores identifiers and timing evidence, without payloads or handler error text. Hard app deletion removes its ledger.

Deleting ledger outcomes records a high-water mark only for their owning subscription. Expired records are excluded even before physical pruning. Window completeness requires a known baseline, a start at or after collection began, a start strictly after the loss boundary, and no expired or unknown outcomes in the requested window. Later complete windows can regain SLO eligibility. Newly created subscriptions establish known ledger baselines; existing subscriptions remain unknown until forward-only reconciliation establishes a new boundary (ADR-1151).

Execution health exposes `window_coverage=durable_execution_outcomes`, window status and reason, collection start, loss boundary, configured retention and earliest evidence expiry. Attempt rates and completion latency use this scope. Current execution states keep the bounded retained-root scope and its existing loss semantics. Window SLOs and alerts use ledger completeness independently of current root truncation; current dead-letter-count alerts still require bounded current-state evidence. Minimum traffic and freshness requirements remain enforced. An incomplete ledger also clears the old `history_complete` flag so older consumers cannot treat incomplete window metrics as complete.

## Consequences

Source cleanup and replay no longer erase recorded window outcomes. Consumers recover independently after ledger gaps leave the window. Legacy subscriptions remain conservatively ineligible. At high event volumes, the ledger adds storage and percentile-query work proportional to the subscription's outcomes in the requested window; owner/window and expiry indexes support those reads and cleanup.
