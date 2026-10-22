# ADR-961: Local delivery health threshold alerts

## Status
Accepted.

## Context
Existing server alert rules cover pending age, pause duration, and retained dead letters. The combined delivery health summary also supports an operator-facing watch of retained retries and sampled circuit/capacity conditions without adding another server alert evaluator or automatic recovery actions.

## Decision
Add opt-in thresholds to `events subscription-delivery-health --watch` and Go SDK `WatchEventConsumerDeliveryHealth`. Monitor oldest pending acceptance age, retained retrying/dead-letter execution counts, pause duration, and locally sampled circuit/capacity restriction durations. Threshold comparisons are inclusive. Zero disables a threshold; at least one must be enabled. Centralize interval, timeout, freshness, count, and duration bounds in pkg/api/limits.go.

Emit one `opened` transition when a fresh condition breaches and one `resolved` when fresh usable evidence clears it. Use a random invocation namespace, incident identity per opening, and monotonically increasing transition sequence. Preserve the same incident ID on its closing transition. Each notification carries the bound app and subscription IDs, including when a mismatched response loses confirmation. Emit `confirmation_lost`, never resolved, on invalid, stale, future, non-increasing, incomplete, or gapped evidence. A fresh baseline after lost confirmation may open a new incident. Failed reads stop the watch after losing active confirmation. Callback failure stops without retry; callbacks receive detached snapshots. The final result carries stop reason, last health evidence, condition states, and latest-pass transitions, not an unbounded notification history.

Bind all reads to the requested subscription and the first valid observed application ID. Freshness is evaluated for each metric's component timestamp; successive samples must advance. Execution thresholds require the recognized retained coverage, no truncation, missing roots or unknown executions, and at least one retained root. Zero retained roots is insufficient evidence rather than proof of no dead letters/retries. Even usable execution observations never establish complete historical coverage.

Capacity duration requires the complete pending sample (at most 50), valid exact recipient identities, pending application scope, and agreement between recipient reasons and sample counts. Truncation prevents confirmation and clearing, including when sampled capacity waits are positive. Circuit duration uses enabled open, half_open, or draining control states, excluding incomplete circuit history. Both duration metrics require at least two consecutive fresh samples and measure local monotonic elapsed time from the first qualifying sample. They describe sampled subscription conditions, not proof of uninterrupted waits between polls or the age of any individual blocker. Paused duration and oldest pending acceptance age retain their existing server semantics.

Watch alerts appear on stderr (JSON lines with --alerts-json); --json writes the final result to stdout. A bounded deadline, interruption, read failure, or callback failure stops the watch and returns a nonzero exit with the final snapshot. Watch-specific flags require --watch. Watches do not implicitly resume, poll indefinitely, send configured server webhooks, acknowledge saved incident outboxes, or change delivery behavior.

## Consequences
Operators gain local proactive threshold notifications and Go callback integration using the existing delivery-health API. This is invocation-local monitoring: restart requires new fresh samples and uses a new namespace. Notifications are not checkpointed or durable; applications needing durable customer alert delivery should use existing server alert rules for supported metrics. No SQL, migration, OpenAPI, alert rule contract, or compliance registry changes are introduced.
