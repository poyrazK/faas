# 1275: MCP readiness incident lifecycle

Status: Superseded by [ADR 1548](1548-independent-mcp-readiness-outbox.md) for incident persistence and delivery sequencing.

## Context

Environment-scoped readiness alerts identify failures but do not notify receivers
when monitoring recovers. Volatile rule state cannot identify an incident through
source outages, delivery failures, or evaluator restarts.

## Decision

Use the existing durable alert-delivery journal as the incident cursor. Failure
and recovery have stable logical event IDs and share an incident ID. Retries
append transport attempts while preserving event identity and original payload.
All transitions and retries compete for a single successor key derived from the
previous delivery ID. Existing unique journal claims admit one successor across
workers; memory storage enforces the same uniqueness across fire and delivery
writers. No side effect occurs before a durable claim.

Pending or failed events retry after cooldown. Completion is ordered: failure,
recovery, next incident. Successfully delivered failures do not repeat during a
continuous breach. Recovery and new incidents bypass the previous firing
cooldown. The signed webhook ID is the stable event ID; the transport timestamp
is refreshed on every attempt. Receivers must deduplicate at-least-once delivery.

Current monitor reads determine recovery only with complete, nonpending evidence
below the threshold and unchanged logical environment/role coverage. Physical
deployments and policy revisions may be replaced if their current monitors are
healthy. Missing roles, missing environments, empty results, and failed reads
cannot close an incident. Retrying an already-recorded event requires no new
monitor evidence. Legacy payloads are not assigned invented recovery events.

An owned-app read API, CLI, SDK contracts, and dashboard expose keyset-paginated
events and delivery attempts, filtered by the full set of observed environments.
A partial index bounds reads to production readiness lifecycle rows. Summaries
retain deployment and policy metadata but exclude observation histories and
credentials. Rule enablement controls evaluation and retry behavior.

## Consequences

Incident state and notification attempts survive process restarts without a new
incident table. An unavailable delivery destination delays later event delivery
for its rule. Changed logical coverage conservatively leaves recovery unverified.
The timeline shares alert-delivery retention and deletion behavior. The feature
sends notifications and does not change serving traffic.
