# ADR-1277: Operator attention for manual initial recoveries

Date: 2026-10-13

## Context

Manual initial recoveries now support verified completion and evidence repair,
but the observer discarded its wait/block reason. Operators had to inspect each
job to discover missing proof and receive no meaningful-change attention alerts.

## Decision

Each submitted preview-mode review has an optional `attention` snapshot containing
the last committed observation time, wait reason, current diagnosis, unresolved
block reason, transition sequence, operator guidance, evidence link/CLI command
and latest notification receipt. Review reads do not refresh this snapshot;
the evidence endpoint remains available for a fresh read-only diagnosis.
Automatic reviews retain their existing automatic fields.

Reuse the pure initial attention transition and webhook capture mechanism.
Missing/unpersisted proof and paused jobs create actionable blocked transitions;
a changed evidence diagnosis creates a new transition. Unchanged polls remain
quiet. Recipients are frozen at capture, including `no_receivers`; adding a
receiver later does not replay an unchanged event. Manual `queue_url` links to
the initial review list. The existing `automation.recovery_queue_blocked` event
now also covers submitted manual initial reviews.

Manual attention clears only after a verified result or exact-generation native
execution progress. Policy changes, disablement, automatic pause, ordinary
admission waits and unverified progress cannot clear it. Clearance indicates an
evidence blocker resolved, not successful execution. Capture durable
`initial_recovery_blocked`/`initial_recovery_cleared` history with frozen review
provenance and the system observer actor. Store snapshot, receipt, history,
outbox and any confirmed result in the same observation transaction.

The manual observer remains bounded and independent of current policy settings.
It does not grant retry authority, spend/refund automatic budgets, change
failure counters, unpause policies or add manual reviews to the automatic queue.
No database schema or history-kind changes are required. The API field is
optional and both generated SDKs and CLI inspection expose it.

## Consequences

Operators can find repair guidance on the review and subscribe to actionable
changes without periodic unchanged alerts. The retained receipt and outbox are
historical; the snapshot is only as current as its observed timestamp. Missing
proof cannot be force-cleared. Existing automatic initial attention behavior
remains covered by regression tests.
