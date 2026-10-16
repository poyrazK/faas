# ADR-1346: Policy-aware scoped route advice

## Status

Accepted. Extends ADR-1345, ADR-976 and ADR-1191.

## Decision

Scoped advice observes the gateway's host-matched and app-wide rule sources for
cache, throttle and async policy. Filter by exact selected app/account and active
rules; do not substitute a default hostname for a named environment. Existing
named headers/CORS overlays do not replace these kinds. The legacy unselected
aggregate advice path retains its existing behavior.

Expose bounded selector/update metadata and an opaque revision fingerprint,
never action payloads or request-condition values. Fingerprints include scoped
identities and update metadata; no private values or account-wide counters enter
them. Compare again before returning; a changed or failed observation cannot
support an absence, duplicate or conflict claim. This is an optimistic desired
policy observation, not a serializable snapshot, write guard or enforcement
receipt. Limit projections to 100 relevant rules and 10 IDs per assessment.

Recommendations keep their telemetry evidence and gain explicit duplicate,
conflicting-configuration, no-matching-selected-app-rule, review-required or
unavailable assessments. Reuse gateway path semantics and cache method
intersection. Compare configured JSON settings; runtime defaults are not
normalized into equivalence claims. Header/structured conditions, log mode,
templates, equal-priority candidates and cross-kind overlaps require review.
Do not expose mutation recipes or approve changes.

Missing capability, failed reads, missing revision metadata and boundedness
failures remain unknown. Disabled drafts, other hostnames, cross-app/global gateway effects,
other gates and serving-cache convergence are not certified. Older native APIs
without policy context remain readable through MCP with explicit unavailable
coverage, with rule bodies stripped. Existing app/environment consent and
identity revalidation remain authoritative.

## Consequences

Agents can explain how advice relates to current selected-app configuration
without assuming telemetry implies a safe policy edit. Future write plans must
obtain native revision guards and perform their own admission checks. No writes,
new data owner, scheduler changes or deployment operations are introduced.
