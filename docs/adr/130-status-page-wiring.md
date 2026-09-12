# ADR-130 · Gregale public status model and publishing workflow

- **Status:** accepted
- **Date:** 2026-09-09
- **Issues:** #276, #277, #599

## Context

The original M8 status surface exposed three Prometheus-derived numbers and a
flat list of internal-daemon incidents. It could answer whether a local metric
was unhealthy, but it did not provide customer-facing capabilities, durable
timelines, maintenance scheduling, trustworthy historical uptime, or stable
public permalinks. It also coupled the public vocabulary to daemon names.

Gregale is currently single-region. Published measurements are error-budget
indicators, not an SLA. The status surface must remain useful when Prometheus
is missing or stale and must never turn missing telemetry into green history.

## Decision

### Public contract

`apid` owns two unauthenticated JSON routes:

- `GET /v1/status` returns overall state and freshness, five capability rows,
  exactly 30 UTC daily observations per row, three current indicators, active
  events, maintenance scheduled in the next 30 days, and at most 20 resolved
  incidents updated in the last 90 days.
- `GET /v1/status/incidents/{public_id}` returns a public event and its
  chronological, append-only timeline.

Responses use `Cache-Control: public, max-age=15,
stale-while-revalidate=45`. The legacy `/status/slo.json` response remains a
projection from the same evaluator and always returns valid JSON.

The stable capability IDs are `api_console`, `deployments`, `app_execution`,
`networking`, and `observability`. Public states are `operational`,
`maintenance`, `degraded`, `partial_outage`, `major_outage`, and `unknown`.
Severity precedence is major outage, partial outage, degraded, maintenance,
then operational. `unknown` describes insufficient telemetry, not severity.

### Persistence and lifecycle

`status_incidents` is extended additively with a public UUID, event kind,
title, impact, affected public capabilities, lifecycle state, schedule/start
timestamps, update timestamp, actor, and create-idempotency key. Existing rows
are backfilled and retain their legacy columns. A control-plane legacy row maps
to all public capabilities.

`status_incident_updates` is append-only. A database trigger rejects UPDATE or
DELETE, and the parent incident uses `ON DELETE RESTRICT`. Titles and update
messages are stored and rendered as plain text, with limits of 160 and 1,024
characters respectively.

Incident transitions are `investigating`, `identified`, or `monitoring` among
the non-terminal states, followed by `resolved`. Maintenance transitions are
`scheduled → in_progress → completed` or `scheduled → cancelled`. Terminal
events cannot reopen. Store methods enforce the same transition rules in
Postgres and MemStore.

### Evaluation and history

An in-process `apid` loop evaluates immediately at boot and every five minutes.
It queries labeled firing-alert vectors, maps daemon/component labels to public
capabilities, maps `warn` to degraded and `page` to partial outage, and overlays
operator incidents and in-progress maintenance using worst-state precedence.
Only operators can declare a major outage. A generic control-plane alert with
no usable daemon label affects all capabilities.

Each successful fresh evaluation inserts one idempotent, UTC-aligned five-minute
bucket per capability. Days with less than 80% coverage are `unknown`; aggregate
30-day coverage below 80% withholds uptime as “Collecting data.” Pre-launch days
remain no-data rather than synthetic operational days. Data older than 90
seconds is stale and stale evaluations do not count as covered rollup buckets.

### Publishing and audit

Admin create, list, and append-update routes live under
`/v1/admin/status/incidents`. Mutations require admin scope, operator allowlist
membership, MFA, recent step-up authentication, same-origin checks, and an
`Idempotency-Key`. Rejections use stable RFC 7807 codes for invalid components,
schedules, transitions, terminal events, titles, and messages.

Every successful mutation emits a durable audit event containing actor, public
event ID, action, prior/new state, and affected capabilities. `gregalectl`
provides incident create/update/resolve/list and maintenance
schedule/update/start/complete/cancel/list workflows, and prints the public
permalink after every write.

### Operations and presentation

Metrics expose bounded evaluator outcomes, last complete rollup time, and event
mutation outcomes. `FaasPublicStatusEvaluationStalled` pages when the evaluator
has not persisted a complete bucket set for 15 minutes.

The React site owns `/status` and `/status/incidents/:id`. The overview is
prerendered and indexed; incident details are `noindex` and excluded from the
sitemap. The page polls every 30 seconds only while visible, preserves its last
snapshot on a refresh failure, and distinguishes initial unavailability from a
delayed refresh. `api.gregale.dev/status` remains the minimal fallback page.

## Consequences

- Customer language is insulated from daemon topology.
- Operators own public narratives while telemetry supplies bounded automatic
  evidence; alert names, daemon labels, and actors never leak publicly.
- Historical uptime starts only when real samples exist.
- There is no hard-delete path for published events or timeline updates.
- Email/webhook subscriptions, a separate status daemon, third-party status
  providers, and multi-region presentation remain out of scope.

## References

- `migrations/20260912133000000_status_public_v2.sql`
- `pkg/publicstatus`
- `cmd/apid/status.go`
- `docs/runbooks/StatusPageDegraded.md`
