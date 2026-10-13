# ADR-953: Diagnostic coverage watch mode

## Status
Accepted.

## Context
Recovery settlement watch can stop while optional diagnostics still have unread or stale recipients. Operators inspecting a large fan-out need a bounded watch whose goal is diagnostic coverage rather than execution settlement.

## Decision
Add `--watch-diagnostics` to recovery status. It enables watch and diagnostics automatically, and selects an independent diagnostic coverage goal. Go watch options expose `WatchDiagnostics`, Node `watchDiagnostics`, and Python `watch_diagnostics`, with the same implication. Default watch behavior remains recovery settlement.

Coverage mode returns when a successful observation pass has a valid sample for every relevant diagnostic recipient and all samples meet the current maximum diagnostic age. Recompute coverage and freshness at the decision time after progress callbacks, then retain that recorded check in the completion result. Disabled age policy accepts valid samples regardless of age. Existing unavailable and acceptance_replaced answers count as valid coverage, not proof of readiness. Unsupported recipients are excluded; an empty relevant selection is complete. Retained samples remain usable under the current age policy.

Monitoring reports `watch_mode` (`recovery_settlement` or `diagnostic_coverage`) and `diagnostic_coverage_complete`. Coverage completion uses stop reason `diagnostic_coverage_complete`; existing timeout, cancellation, read_failure, retry_exhausted, validation_failure and callback/checkpoint failure reporting remains available. `acknowledged_executions_terminal` continues to describe receipt execution settlement independently. CLI coverage success exits zero even if delivery has failed or is unresolved, while other modes keep existing delivery exit semantics.

Keep existing 50-recipient diagnostic batch rotation, retry/refresh cursors, receipt observation limits, read deadline, polling interval, overall timeout and checkpoint ownership. Coverage mode makes batch transport/decode failures explicit and uses the existing shared transient read retry budget and Retry-After handling. Permanent failures fail immediately. A failed diagnostic batch preserves its partial result and retry cursors. Independent item failures or invalid recipient evidence keep rotating until coverage completes or the caller's deadline is reached. Optional diagnostics outside coverage mode remain nonfatal.

## Consequences
Diagnostic completion can precede or follow delivery settlement. It is a read-only inspection goal and does not submit recovery or claim handler success. Small age limits or unavailable receipt evidence can prevent completion; timeout returns/checkpoints the current partial coverage. Freshness is meaningful only at its recorded local check time. No backend endpoint, SQL, migration, new quota or compliance registry changes are needed.
