# Operator alert workload summary

Status: Accepted, 2026-10-16. Extends retained episodes and triage history.

## Decision

GET `/v1/admin/obs/incidents/platform_alert/summary` reads all physically retained
platform-alert episodes. This fixed scope accepts no query parameters, page
cursor or historical window. Deployment, job and node incidents are outside the
scope. Expired episodes awaiting cleanup still count; retention health reports
the exact cleanup backlog and cutoff in the same snapshot. This is retained
workload evidence, not first-ever incident analytics or notification delivery.

The five grouping dimensions are observed status (firing/resolved/unknown),
severity (critical/error/warning), acknowledged, owned and currently snoozed.
Return only nonempty intersections, at most 72 groups, in lexicographic dimension
order with false before true. Total counts cover every retained row independently
of inbox pagination. An episode needs attention when it is unresolved, unsnoozed,
and either unacknowledged or unowned; count that union once. Unknown unresolved
episodes remain eligible so an observation outage cannot clear the workload.
Acknowledgment, ownership and snooze do not change observed alert status.

Return the oldest unsnoozed unresolved unacknowledged and unowned episode
references, sorted by activation time then immutable episode ID ascending.
Age is whole elapsed seconds since `started_at`, clamped to zero for small
future clock skew. It is not assignment age, acknowledgment latency or an SLA.
References contain status, severity and evidence links without actor identities.
Snooze expires strictly at its saved deadline and requires no writer or revision
change. A recurrence starts its own workflow state and remains independently
counted alongside its retained resolved predecessor.

PostgreSQL uses one read-only repeatable-read transaction for collection state,
retention state/counts and grouped workload. A SQLC window aggregate plus
DISTINCT ON returns only bounded count/oldest projections, never fleet rows or
unbounded ID arrays. Memory holds one mutex and accumulates bounded buckets.
The configured collector-enabled value and one UTC microsecond `as_of` clock
are fixed for the request; the query budget is five seconds. Existing schema and
indexes suffice; no migration, new producer or lifecycle writer is introduced.

Before any successful collection, counts are null, groups are empty and oldest
references are null. A successful quiet collection gives known zero totals.
Collection health carries disabled/never-collected/failed/stale/unknown/current
metadata. Disabled collection and stale or missing rule observations classify
unresolved retained episodes as unknown. Confirmed historical resolutions stay
resolved. Read failures return 503 without database details, rather than counts
or partial results. Cleanup health can be stale while counts remain exact for
physically retained rows; the summary does not promise a complete fixed-duration
history or a current live fleet when its collector is unavailable.

The API retains the existing operator read scope, allowlist and MFA boundary.
`gregalectl obs summary --json` exposes the typed response; human output prints
source states, totals, groups and oldest evidence paths. Missing pre-collection
counts print as unknown rather than zero. The public tenant Go SDK excludes this
operator route in both existing exclusion maps; the canonical OpenAPI and
generated Node admin client document it. Frontend and rollout are separate work.

## Validation

Check memory and PostgreSQL counts beyond one inbox page, dimension intersections,
snooze expiry without writes, stale/failed/disabled collectors, missing rule
observations, recurrence isolation, canceled reads and database failures. Race
retention and triage changes against aggregate reads to verify snapshot coherence.
Shared Store conformance covers the new method. Test oldest tie ordering and age,
HTTP authorization and query rejection, CLI unknown/zero and failure handling,
SQLC drift, OpenAPI/schema parity, generated Node output and configured lint.
