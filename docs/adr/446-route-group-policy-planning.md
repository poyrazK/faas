# ADR-446: Route-group policy plans and captured impact

- Status: Accepted
- Date: 2026-10-01
- Related: ADR-438 (transactional policy apply), ADR-445 (family coverage)

## Context

Captured family coverage identifies resource-policy gaps, but concrete-only
planning requires customers to translate templates into gateway selectors and
manually check overlapping requirements and public exceptions.

## Decision

Promote version 2 requirements and coverage reports into shared API/SDK types.
Keep version 1 concrete artifacts at version 2. Group artifacts use version 3,
with an explicit app-owned captured deployment ID and contract SHA-256. Missing,
truncated, unparsed, referenced or otherwise incomplete inventory stays unknown.

Read the capture in the consistent policy snapshot. During apply lock the
account, app, rules, selected deployment and existing captured-contract row.
Recompute the complete reviewed plan before staging changes, and verify persisted
rules against the same locked capture before committing the durable receipt.
Capture updates/deletion cannot race that verification. Idempotent replay happens
before capture lookup so historical receipts remain recoverable after deletion.
Use sqlc ownership-filtered queries; no new tables or migrations are needed.

Generate throttle/budget selectors for captured families, scoped to one platform
host and method. Replace whole-segment parameters with gateway stars, retaining
explicit disclosure that trailing stars also select descendants and wildcards
cover uncaptured/future paths. Never claim the capture bounds all traffic impact.
Update an existing action only when its selector equals that generated selector.
Otherwise use the existing precedence, quota, account-limit and allowlisted
proposal machinery. Do not change authentication, route or rewrite policy.

Combine overlapping ceilings before proposing. Conflicting identity/missing-key
requirements remain manual. Prove selected policy for every intersecting captured
operation before and after; include changed selections and intersecting preserved
public exceptions in impact. Independently prove public request-context invariance
because public intent suppresses requirement checks. Any changed exception or
unknown impact blocks that proposal. Recheck the complete inventory after each
proposal and reject worsening of any already satisfied check.

Share coverage work, bytes and findings across planning rechecks; cap changes at
32 in the central limits table. Exceeding any bound discards all proposals and
produces blocked unknown coverage, never partial successful evidence.

Normalize group/route/public order for deterministic intent. Public rationale is
local commentary: replace it with a fixed declared_public_exception marker before
CLI transmission and again on the server. Shareable artifacts and receipts do not
contain original commentary. Reason text does not affect policy fingerprints.

## Consequences

ADR-447 adds opt-in budget consolidation within declared group prefixes. The
individual family strategy below remains the default and the throttle strategy.

Customers can review a remediation batch with full captured coverage and apply it
atomically without crafting individual API writes. Conservative path proofs leave
conditional rules and conflicting families unresolved. Runtime authorization,
header overrides, framework normalization and shared bucket admission effects
remain outside configured-policy verification. Newly captured routes need another
coverage check even when an existing generated wildcard might select them.

## Validation

Planner tests cover overlapping ceilings/conflicts, descendant impact, family
updates, public exception protection (including hidden request-context mutation),
unknown inventory and whole-plan bound failure. CLI tests cover deployment binding,
impact output, private-rationale removal and reviewed apply forwarding. API and
memory/PostgreSQL transaction tests cover stale captures, ownership, rollback,
receipt replay after capture deletion and concurrent capture-writer locking.
