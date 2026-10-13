# ADR-955: Selected consumer diagnostic summaries

## Status
Accepted.

## Context
Large recovery selections need a compact view of which subscription has sampled routing blockers and where diagnostic coverage is incomplete.

## Decision
Add `consumers` to diagnostic progress in the CLI and Go, Node and Python SDKs. Group relevant selected application recipients by normalized subscription ID, sorted deterministically. Count selected targets, fresh/stale/unread samples and validated sample statuses. Separate fresh and stale waiting-reason maps; unavailable, replaced and invalid samples contribute no waiting reasons. Count newly compared and changed recipients, excluding retained comparisons. Identify the oldest sampled waiting acceptance with its exact guarded query, observation timestamp, freshness and waiting reason. Tied acceptance times retain selection order.

Recompute summaries from validated rows using the same progress check time and configured diagnostic age policy on enrichment and freshness rechecks. Never trust checkpoint aggregates. Exclude unsupported recipients. CLI fingerprints omit changing observation timestamps but include consumer-specific coverage, reasons, changes and oldest acceptance identity.

## Consequences
The output describes selected samples, not account-wide totals or delivery outcomes. Stale reasons remain visibly historical. Acceptance age cannot establish blocker duration. Changed means any confirmed routing observation change, not necessarily a blocker clearing. No additional requests, backend scan, SQL, migration, limits, watch completion or recovery action changes are needed.
