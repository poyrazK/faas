# ADR-1245: Environment-attributed management MCP observations

## Status

Accepted. Extends ADR-1241 and ADR-1244.

## Context

App route counters and discovered-route catalogs aggregate deployment scopes.
Operational summaries also include app-wide monitoring and restart requests.
Returning those aggregates through an environment-restricted connection would
expose neighboring releases. A worker image becoming live does not establish
consumer replacement, drain completion or queue processing.

## Decision

Add explicit selectors to existing route and operational-summary reads:
`scope=default` or one registered `environment`. Preserve selector-free legacy
CLI/dashboard reads. MCP always requests an explicit selector, verifies immutable
app/project/environment identity, and never falls back to an aggregate response.
Unknown, duplicate and conflicting selectors fail closed.

Scoped route history joins stored request observations to owned deployments in
the selected scope within the existing five-minute health observation window.
Apply the route bound after scope filtering. Report attributed history as partial;
unsupported or unavailable telemetry remains unavailable. No aggregate Prometheus
or app-wide durable discovery catalog is used as a scoped fallback.

Scoped operational summaries include independently collected scoped health and
incident metadata attributable to a deployment in that scope. Filter rollback
operations before bounding the list. Read explicitly scoped restart handoffs only;
select the latest app/wake handoff before scope and terminal-state filtering.
App-wide/unattributed requests remain excluded, and unsupported readers remain
unavailable. App-wide route-monitor budget evaluations do not become scoped
monitoring merely because a summary is requested for an environment.

Add a read-only exact worker rollout observation endpoint. MemStore collects
under its lock; PostgreSQL uses one statement snapshot. Select the native newest
live generation in this scope and count candidate and retired worker residents,
including draining, migrating, snapshotting and warm states. Generation residency
converges only with a running selected generation and no remaining startup, drain,
other or retired residents. This does not establish queue processing, replica
policy satisfaction, delivery acknowledgement or external consumer completion.
No controller, queue acknowledgement, instance or VM mutation is introduced.

## Consequences

Restricted agents can inspect scoped route history, recovery work and worker
generation progress. Missing evidence is not a healthy/empty result. Existing
aggregate readers retain their behavior, while new MCP reads require the updated
API. Tests cover ownership, scope-before-limit behavior, handoff replay, residency
transitions and absence of read-induced activation. Native VM rollout qualification
still requires a dedicated KVM host; this cloud workspace has no KVM device.
