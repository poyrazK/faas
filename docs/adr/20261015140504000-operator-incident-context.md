# Operator incident impact and related context

Status: Accepted, 2026-10-15. Extends operator incident evidence and retained
platform-alert episodes.

## Decision

Add the read-only admin endpoint
`GET /v1/admin/obs/incidents/{kind}/{id}/context` and
`gregalectl obs context --incident type/resource-id`. Existing admin scope,
MFA, and email allowlist gates apply. No frontend or rollout changes are part
of this implementation.

Context describes **current potential exposure**, not historical blast radius
or root cause. `as_of` timestamps current database relationships separately
from `incident_observed_at` and the fixed `since`/`until` timeline window.
Resource status is its present database state. `confirmed_failure` means an
explicit failed deployment or failed/dead-letter job run; it does not attribute
that failure to the incident or prove customer request failure.

Deployment incidents resolve their current app/account. Node incidents include
non-deleted apps owned by the node or with waking, cold-booting, running,
draining, or warm instances there. App scope never expands to other apps on a
shared node. Related nodes include current app owners and those instance
placements; these additional nodes do not expand the workload set recursively.
Live deployments and deployments created in the timeline window are included,
plus the referenced deployment itself. Tenant counts use account IDs, matching
the existing operator tenant API.

Jobs have independent workload identity. Node scope also includes non-deleted
jobs with explicit resident job-task instances on that node, and their owning
accounts. Job scope exposes current job-instance nodes. A job-run incident links its verified
job/account and other retained runs of that same job within the window; the
referenced run is always included. Account membership alone never links jobs
to apps or unrelated runs.

Retained alerts use a verified app reference first, then a verified node
reference. App/account conflicts and missing selected references make scope unknown;
there is no fallback to a broader node/account scope. A node plus account
reference restricts apps to that account. Account-only and global alerts have
unknown scope because their labels do not establish workload relationships.
`counts: null`, `scope.reason`, and `incident_scope` in unavailable sources
distinguish unknown scope from a verified empty relationship set. Alert status
remains unknown when collection is unavailable, independently of the current
database relationships. No live Prometheus query is needed.

## Snapshot, bounds, and pagination

PostgreSQL resolves the root, verifies scope, counts relationships, reads the
resource page, and collects its timeline in one read-only repeatable-read
transaction. MemStore mirrors this under one lock. SQLC owns production SQL.
Existing primary, placement, job-run, event-subject, operator-reference,
deployment-audit, and operator-intent indexes support these reads. No schema
change or new writer is needed.

Counts cover the full relationship set, independently of the resource page.
Resources sort by kind and UUID with a keyset cursor; default 25, maximum 100.
The cursor binds the canonical incident, so a legacy alert series that moves
to another episode cannot silently continue a page for the old episode.
Cursors expire after fifteen minutes and preserve the original history upper
bound. Each page reads current relationships afresh and carries its own
`as_of`; it is not a database snapshot held across requests.

The timeline covers the exact incident/scope targets plus returned resource
IDs, explicitly marked `incident_scope_and_returned_resources`. Tenant/account
rows are excluded from timeline targets. Entries match exact event subjects,
existing operator-action resource references, deployment audit IDs, or operator
intent targets. Source and resource IDs, match basis, closed metadata, and
validated trace links are returned; actors, payloads, reasons, commands,
images, environment values, and free-form errors are omitted. Entries are
deduplicated by source/ID, preferring direct event subjects, then ordered by
time/source/ID. Each indexed source/target is bounded before the final merge.

The fixed window is the last 24 hours; timeline default 25, maximum 100, with
explicit truncation. Paging resources changes timeline coverage accordingly.
This is related evidence, not a complete history or a causal chain. Historical
alert transitions remain available through the existing evidence link.

Limits and the five-second request budget live in `pkg/api/limits.go`.
Invalid/expired/cross-incident cursors return 400, missing/deleted resources or
unretained alert IDs return 404, and database failures return 503. Failures
never become successful empty context.

## Validation

Memory/PostgreSQL tests exercise scope, exact counts independent of pages,
current placements, excluded deleted/terminal workloads, app/node/account
isolation, confirmed failure semantics, job identity, sanitized timeline
correlation, deduplication and bounds. Domain/API/CLI tests cover trace links,
cursor binding/expiry, unknown alerts, unavailable reads, and authorization.
