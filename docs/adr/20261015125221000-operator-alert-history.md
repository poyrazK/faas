# Durable operator platform-alert episodes

Status: Accepted, 2026-10-15. Extends ADR-091 and operator incident evidence.

## Decision

`apid` collects the configured Prometheus `/api/v1/rules?type=alert` every
30 seconds under a ten-second budget. It writes only sanitized platform-alert
projections to PostgreSQL. No new ingress, credentials, notification receiver,
frontend, or deployment step is required by the backend change. Collection is
disabled when the existing Prometheus client is not configured.

A complete observation requires bounded valid JSON, no warnings/infos, known
alert states, healthy rule evaluations within the existing two-minute freshness
budget and thirty-second clock skew allowance, and consistent active-alert
arrays. Empty/missing groups, missing rule data, unhealthy/stale evaluations,
and source failures never authorize recovery. Watchdog and info-only alerts
remain excluded. One malformed rule rejects the whole poll conservatively.

Rule identity hashes file, group, name, expression, duration, and rule labels.
Changing or removing a rule leaves its previous unresolved episodes unknown;
it does not declare recovery. An unchanged, fresh, healthy rule can resolve an
old firing episode only when that episode is absent from its firing set.
Resolution is the evaluation time at which recovery was observed, not a
claim to know the precise instant of recovery between polls.

The existing SHA-256 label identity remains the series ID. Episode identity
also includes rule identity and Prometheus activation time, so recurrences
have separate immutable links. Activation time can include the pending
period. `first_seen_at` records when this collector first observed firing;
`last_seen_at` is its latest confirmed firing observation. Evidence's firing
entry uses the first observation time. Raw labels, expressions, annotations,
descriptions and free-form errors are never persisted. Resource UUIDs and
runbook URLs retain the existing validation and documentation-only policy.

## Ordering and durability

A singleton collection row serializes writes and cleanup. Collection-start
watermarks reject delayed batches and failures. Each rule also retains its
latest evaluation and sorted episode set. Older evaluations and contradictory
sets at the same evaluation cannot resolve or reopen episodes. Duplicate
observations are idempotent, and resolved episodes never reopen. Episode
updates, recoveries, rule watermarks and collection success commit atomically.
Reads use a repeatable-read snapshot for episode data and collection health.
MemStore mirrors the same rules under one lock and copies returned pointers.

A successful poll updates confirmed current episodes. A failed poll, stale
collection, or episode whose rule was absent from the latest successful poll
projects `status: unknown`, retaining its last known firing evidence. Resolved
records remain `resolved` and historical regardless of current telemetry.

## Read contract

Existing admin scope, MFA and allowlist gates protect all reads.
`GET /v1/admin/obs/incidents?type=platform_alert` includes unresolved episodes
and resolutions within `since` (up to thirty days for this type), ordered by observed time and ID with bounded
SQL pagination. `alert_status=firing|resolved|unknown` requires that type.
Resolved observed time is the confirmation time; ongoing observed time retains
the activation clock. `alert_episode` adds series ID, activation/observation
clocks, resolution, and `observation_state=current|unknown|historical`.

Evidence links use episode hashes. Existing series-hash links resolve to the
latest retained episode, while episode links stay attached to their original
occurrence. Retained reads work without contacting Prometheus. Evidence
contains bounded firing/resolution entries rather than raw payloads.
Before the first successful collection, the existing live lookup remains as
a compatibility view, marked unknown with `platform_alert_history` unavailable.
Database failures return 503 instead of silently using a live fallback.

`GET /v1/admin/obs/health` adds `alert_history` with current/unknown state,
last attempt, last success, and a closed reason vocabulary. The incident list
and unknown evidence report `platform_alert_history` in unavailable sources.

## Bounds and retention

Limits live in `pkg/api/limits.go`. A poll accepts at most 4096 rules and 4096
firing episodes, with the existing eight-MiB Prometheus response bound.
Each pass prunes at most 500 episodes whose resolution or last confirmed
firing observation is older than thirty days. This includes expired unknown
records without manufacturing a resolution. Recently observed ongoing alerts
remain retained even with an old activation clock. Orphan rule watermarks are
also pruned in bounded batches after their episodes expire. There is no
backfill of alerts that resolved before collection began, and episodes which
start and resolve entirely between polls may not be observed.

The additive migration creates three tables and supporting indexes. It does
not rewrite workload tables or change scheduler/controller ownership. Normal
backend migration/startup activates collection; rollout remains a separate task.

## Validation

Memory/PostgreSQL contract tests cover firing, duplicate observations, same-
evaluation contradictions, delayed/concurrent batches, recurrence, missing
rules, outage freshness, legacy aliases, immutable episode links, cursor ties,
filters, input limits, strict/capped retention, restart persistence and errors.
Collector/client tests reject incomplete/stale/oversized/failed observations and
pin safe projections. API tests verify offline retained reads, recovery history,
unknown state, health, pagination, validation, authorization and fail-closed reads.
