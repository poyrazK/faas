# ADR: Typed pipeline tools with portable start receipts

Status: Accepted (SDK preview)

## Context

Typed pipelines already compose Jobs, files, human gates, events and schedules.
Gregale's MCP hosting and management tools already own transport and publication.
Applications still hand-write business tool schemas and reconnect-safe invocation
logic, risking duplicate starts and stale authorization.

## Decision

Add matching Node/Python preview definitions over existing pipeline contracts,
with a transport-neutral MCP descriptor/call adapter. Expose start, status,
result, cancel and explicit reconciliation. Existing MCP hosts own lifecycle,
negotiation, authentication and transport. Do not build an agent harness or a
parallel hosted tool service. Default to the existing 2025-11-25 wire surface;
2026-07-28 mode adds complete result discriminators after host negotiation.

Generate tool input/output schemas from the existing portable Job contract.
Bound frames and gate reads. Require current application authorization on every
operation, including duplicate starts. Derive a stable subject and owner from
trusted application context. Treat UUIDs and links as selectors, not capabilities.

Use shared application-owned PostgreSQL start receipts. Only an acknowledged
new reservation can dispatch. Unknown reservations/native replies never permit
automatic resubmission, even beyond native key retention. Retain only identity,
configuration/input digests and accepted checkpoint. Add an additive `tool`
trigger discriminator to pipeline envelopes, so reconciliation checks this
request's exact provenance rather than accepting another equal-input run.
Introduce no native endpoint, migration, admission owner or plan quota.

Read native run state and validate output before returning results. Explicit
cancellation stops orchestration, not already accepted Jobs or business effects.
For pending human gates, verify run/owner/scope/contract/input, resolve database
expiry, and return only a fixed authenticated review URL and safe metadata.
Do not expose approval decisions or review data as agent tools.

## Consequences and evidence

Applications explicitly migrate receipts and upgrade workflow drivers before
accepting tool envelopes. Query adapters require acknowledged autocommit on one
primary. Business authorization binds supplied inputs to trusted immutable data;
subject/scope/name changes and receipt pruning require a migration strategy.
Callbacks and database drivers must respect total deadlines. Existing callers
need no API migration and native failed workflows remain non-continuable.

Shared fixtures exercise schemas, descriptors and canonical receipts across
languages. Unit tests cover concurrent calls, restart, uncertain writes/starts,
current access, binding conflicts, approval expiry, results, cancellation and
deadlines. Package consumers check generic APIs. The CSV example uses real
PostgreSQL and Chrome with MCP/native protocol fixtures and mixed workers;
deployed publication and VM qualification remain separate acceptance gates.
