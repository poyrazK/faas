# ADR-1533: Read-only MCP operations dashboard

## Status

Accepted; projects the existing MCP operations contracts into the dashboard.

## Context

Connection health, catalog drift, OAuth recovery and policy/admission evidence
are available through APIs and CLI commands but lack a shared browser surface.
Operators need freshness and source completeness alongside status to distinguish
healthy observations from absent or unavailable evidence.

## Decision

Add a session/MFA-protected GET dashboard page using account-scoped store reads
and the existing catalog freshness evaluator. Show connection issues, retained
retirement issues for deleted apps, per-role observation times, gateway replay
and latest accepted/rejected admission receipts for each newest deployment.

Bound per-application work to 25 rows and a five-second read context. Paginate
application rows and connection-health issues independently. Reuse ListApps for
the account inventory; this does not introduce a new paginated store contract.
Mark source failures and incomplete inventory explicitly. Do not invent healthy
states or substitute empty history for an unavailable source.

Use view models containing only safe status metadata. Never render OAuth sealed
values, catalog bodies/diffs or raw backend errors. Escape rendered strings and
construct internal links locally. Direct receipt links check account/deployment
ownership. Reuse protected request-debugger, log and access-rule surfaces.

## Consequences

The page is read-only, does not initiate provider exchanges or tool executions,
and requires no new CSRF mutation flow. Use CLI recovery procedures separately.
Admission summaries cover newest deployments, not all historical releases, and
accepted receipts do not attest to future fleet/provider health. The page does
not aggregate protocol logs into charts or change hosting's preview maturity.
