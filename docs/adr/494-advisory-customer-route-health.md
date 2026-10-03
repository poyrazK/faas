# ADR-494: Advisory customer route health

## Status

Accepted — 2026-10-03.

## Context

ADR-454's selected-route canary checks compare aggregate telemetry. ADR-493
exposes request-time customer attribution for route changes, but aggregate
health can still hide a tenant-specific regression. Customers need comparable
per-identity production evidence without inferring historical tenant membership
or expanding rollout enforcement before this evidence is assessed.

## Decision

Extend live route-health reads with opt-in advisory customer evidence. Tenant
(default) and API consumer are separate selectable dimensions. Resolve recorded
IDs within the telemetry account/app scope; include revoked consumers and never
join today's tenant-consumer membership. Reuse exact normalized route selectors,
immutable deployment pair, shared closed windows, anchors, weighted counts,
latency estimates and consecutive-signal thresholds from aggregate health.

Compute aggregate and customer evidence in one repeatable-read transaction.
Cohorts are the union of both deployments; missing sides remain explicit zero
observations and therefore unknown comparisons. Bound output at 20 identities
per selected route, ordered by candidate 5xx count, combined volume, then UUID.
Aggregate attribution/counts precede output bounds. Return omitted-customer
volume, cohort count and cap flags. Unattributed/unresolved, sparse, empty or
bounded inventories prevent a healthy summary; confirmed observed regressions
take precedence. Coverage remains observed_only.

IDs require explicit customer_details. The CLI also strips unexpected IDs before
rendering/export and validates cohort counts against the shared aggregate report.
Expose additive Go, Node and Python SDK options. Do not persist customer evidence
or identities in rollout history, audits or webhooks. The canary advancement and
recovery paths continue to use aggregate evidence only, and normal live reads
avoid the customer query. No schema change is required.

## Consequences

Developers can locate an affected customer/route and compare production behavior
without manual log joins. Low-traffic identities often remain unknown, and
sampling/retention never provide a full-capture denominator. The bounded ranking
can omit a regression; cap/omitted-volume fields disclose that limitation. These
reports provide investigation evidence before any future customer enforcement
policy is introduced.
