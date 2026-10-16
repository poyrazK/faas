# ADR-1456: Environment-scoped route awake and cacheability diagnostics

## Status

Accepted. Extends ADR-1187, ADR-1188 and ADR-1237.

## Decision

The awake/advice read APIs retain aggregate behavior when neither selector is
supplied. Explicit `scope=default` or `environment` selects only historical
records attributable to that environment. Reject duplicate, mixed or invalid
selectors and resolve effective workload settings through the existing owner.
Responses carry exact app, scope, project and environment identities and partial
retained-deployment-history coverage. Revalidate named registry identity after
collection; the MCP bridge additionally checks consent and response identities.

Request attribution uses retained deployment IDs. Usage attribution follows
retained instance IDs to their deployment. Named deployments must be pinned to
workload specs for the current environment ID; matching a reused slug is
insufficient. Missing attribution is unknown and excluded, never charged or
assigned proportionally to an environment. Scoped totals are partial diagnostics,
not a new billing ledger or proof of no activity.

The previous content observer key mixed deployments. New comparisons include an
exact deployment ID and use the versioned `deployment_v1:` outcome vocabulary.
Only those outcomes establish learned evidence for scoped advice; aggregate
advice continues to understand both vocabularies. Publish no bodies or hashes.
Apply the additive database constraint and compatible receiver before rolling
out gateways; older receivers drop unknown versioned outcomes to empty, which
loses evidence without inventing it. Do not backfill legacy provenance.

Scoped advice does not borrow app-wide wake/queue models, custom domains or edge
policy. It returns diagnostic suggestions without ready-to-apply rule bodies,
with an explicit warning that environment policies were not evaluated. Normal
plan gating, ownership, telemetry retention and bounded output still apply.
MCP always requests explicit scope and rejects aggregate or mismatched results.

## Consequences

Remote environment-consented agents can inspect these reports. Attribution gaps
and rolling-upgrade legacy comparisons reduce evidence and remain visible as
partial coverage. Retention may remove history needed for attribution; no
fallback to the aggregate is permitted. No scheduler or VM lifecycle ownership
changes, deployment mutations or active probes are introduced.
