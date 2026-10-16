# ADR-1241: Reviewed named-environment staging and scoped health

## Status

Accepted. Extends ADR-1240.

## Context

Management MCP can promote existing environment releases, but its image plans and
health reads originally covered the legacy default scope. An agent needs to stage
an image in a named environment, exercise it there, and inspect attributed health
before reviewing a production promotion. Manual traffic redistribution must remain
inside that environment.

## Decision

Extend app release review with scope, immutable project/environment identities,
workload revision/hash and protection state. Named reviews hash the app intent,
registry entry, desired workload revision and legacy route override. On reviewed
zero-traffic insertion, lock the registry, app and route intent; compare review
before inserting or superseding anything. The API resolves target settings before
admission. An environment without a desired head freezes those resolved settings
as its first immutable revision in the insertion transaction. Return a staged
review of the committed configuration with the candidate; retain it in encrypted
MCP plans for retries and promotion.

Add a reviewed traffic API guard that compares configuration and release identity
inside the traffic transaction. Require a serving-release guard and 100% target
traffic. Reject protected named targets on this manual path: approved environment
promotion remains their workflow. Redistribute weights and validate their sum only
among live deployments in the target scope. Keep other scopes' weights unchanged.

Add optional project/environment selection to MCP image plans and app/health
inspection. Enforce existing per-app and per-environment consent before and after
reads, and on every apply/retry. Named plans retain their committed workload review
across restarts. Older APIs cannot silently substitute a default-scope review or
health response.

Extend on-demand app health with a registered environment query. Select that
scope's serving releases and latest deployment; readiness findings only reference
selected serving deployments. Fetch request metrics by their exact deployment
IDs, retaining unknown, coverage, freshness and plan restrictions. Independent
health history collection continues to record the default scope. Named and remote
MCP metrics project those scoped request counters in the fixed five-minute health
window. Existing exact-deployment logs already enforce consent. Aggregate route
telemetry and operational summaries remain unavailable to restricted connections.
Zero-traffic candidates are staged rather than serving; health never wakes or
probes them.

## Consequences

Agents can review, stage, wait, promote within an unprotected staging environment,
inspect its health and request evidence, and then use the existing approved
environment promotion workflow. This covers digest-pinned request-mode images;
source builds and service/worker rollout contracts remain separate. Health reads
are bounded observations, not active smoke checks or a qualification issuer.
