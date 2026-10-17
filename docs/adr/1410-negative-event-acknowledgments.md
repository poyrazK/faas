# ADR-1410 · Explicit negative acknowledgments for deferred deliveries

Status: accepted

## Context

A durable consumer that knows processing failed currently waits for lease expiry
before the platform can retry or dead-letter that consumer's delivery.

## Decision

Add token-fenced retry and dead-letter callbacks during the live offered or awaiting
phase so they can race the HTTP 202 handoff. Reuse captured backoff and attempt
limits. Transition the invocation, release reserved quota, record a history outcome
and persist the original response in one transaction.

The rejected phase retains a digest of the action and bounded non-sensitive reason,
plus its response snapshot. Identical repeats return the saved response until the
state or claim is replaced. A new offer clears the receipt. Late scheduler handoff
returns the existing disposition without reopening the claim. Renewal stays stale.

Preserve quota lock order, keyed ordering, source snapshots and at-least-once
semantics. External effects are not undone. Require app authority and the current
token. CLI input uses a bounded protected JSON file. Expose SDK operations and
retain attempt diagnostics separately from expiry-rate counters.

## Consequences

Known failures recover promptly per consumer. The acknowledgment record gains one
operational JSON column. Upgrade apid and every scheduler before use; older offer
SQL fails closed against a saved rejection receipt. History clients must recognize
the two new outcome kinds. No production rollout is part of this change.
