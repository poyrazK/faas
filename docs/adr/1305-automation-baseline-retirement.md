# ADR-1305: Audited retirement of named automation baselines

## Status

Accepted

## Context

Unused baseline names consume the 20-name quota and keep suite groups pinned.
Deleting promotion receipts would lose trust history and allow old requests to
create a name again. Retirement must serialize with promotion and retain evidence
of the authenticated actor's decision.

## Decision

Add explicit PUT `live-baselines/{baseline}/retirements/{retirement_id}` with a
positive `expected_revision`. Deploy-write scope, MFA and verified email match
promotion. Record an immutable attributed retirement receipt at revision N+1,
referencing the last promotion at N. An identical retirement ID and semantic
intent replays its original receipt; changed identity, actor or revision fails.
Names are permanently reserved after retirement. New promotion IDs cannot reuse
them. Retained original promotion IDs still replay historical receipts without
moving the head or reactivating a name.

Current-name GET returns HTTP 410 with a clear retired problem. Named CI gates
return `baseline_retired` with exit 4, without reading a comparison. Active discovery
excludes retired names. History GET continues to return up to 100 promotion
receipts plus the retirement receipt. Gate resolution never falls back to a
different name. As with promotion, a gate that already resolved a receipt keeps
that snapshot; retirement does not cancel a comparison already in progress.

Retirement frees an active-name slot and releases only this name's suite pin.
Other active names and the managed-suite recovery window still protect shared
suites. Normal retention may subsequently prune unpinned evidence; retirement
itself does not delete evidence or refresh timestamps. Explicit test-evidence
clearing preserves audit; deleting the automation cascades its audit.

Bound metadata at 20 active names and 100 lifetime names per automation, with
limits centralized in `pkg/api/limits.go`. The lifetime cap prevents unlimited
archived receipts while preserving every retired name's bounded history.
Retirement works without a live deployment or retained evidence.
Retirement requires an explicit confirmation in dashboard and CLI, and a stable
UUID plus expected revision for uncertain retries. Read paths never retire.

Memory holds the existing store mutex throughout the decision. PostgreSQL uses
SQLC, app admission locking, ownership locking, and a no-op write to the automation
row in the same repeatable-read transaction. The write creates a row version
without changing the saved target or timestamp, forcing stale waiting writers to
abort before promotion/retirement or cross-name quota decisions can both commit.
Concurrent identical retirement requests recover the committed receipt only
after rollback, matching promotion retry semantics.

## Consequences

A retired name cannot be restored; use another active name. Known-name history
remains available after evidence disappears. Active collection reads stay bounded
and compatible. Archived-name discovery and a future reviewed reactivation
protocol are separate features. No automation execution or VM changes occur.
