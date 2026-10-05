# ADR-463 · Managed PostgreSQL provider health

- **Status:** accepted
- **Date:** 2026-10-01
- **Decision:** Collect read-only provider metadata for ready databases on a
  separate durable schedule and expose cached health through database GET/list.
- **Why:** Lifecycle reconciliation stops at `ready`. Provider outages, resource
  loss, and configuration drift can therefore remain invisible indefinitely.

Health is independent of lifecycle state and the provisioning rollout gate.
An existing ready database stays ready while its health becomes degraded or
stale. Monitoring never recreates a database, changes intent, obtains credentials,
opens SQL connections, wakes compute, or repairs privileges. Adapters implement
an optional `DatabaseObserver`; missing support reports `observer_unsupported`
instead of falling back to lifecycle `Inspect`, which can repair restored roles.
Neon observation reads project, branches, and endpoints with three concurrent
GETs. It ignores historical operation failures and treats ready suspended
compute as healthy. Current resource readiness and the complete desired spec
remain the checks; this signal does not prove SQL connectivity or data integrity.

The collector runs when the managed provider registry is configured. The
`health` policy defaults to enabled, a 60-second interval, and 300-second
freshness. Operators can disable collection explicitly. Interval bounds are
60–3600 seconds; freshness bounds are 120–86400 seconds and at least twice the
interval. These are operational pacing controls, not customer quota changes.

Each process sweeps every 15 seconds, claims at most 20 rows sequentially, and
spaces checks by at least one second. It acquires each 30-second durable lease
immediately before a provider observation with a 10-second deadline. Replicas
share claims; cancellation leaves the lease to expire without publishing a
provider failure. Failed metadata requests retry with exponential backoff up
to 15 minutes (or the configured interval when larger). Valid but degraded
observations, including provider failure states and spec drift, reset request
failure backoff. There is no fleet-wide API rate limiter: replicas can each
start one observation per second. Large catalogs require measured tuning;
freshness and heartbeat alerts expose capacity and outage gaps.

A separate additive `managed_postgres_health` table owns leases and observations.
Claims lock the catalog row with `SKIP LOCKED`. Completion locks that row before
updating health, and verifies a live lease and current account, backend,
fingerprint, provider identity, and desired generation. Deletion and replacement
therefore fence late results. Placement changes hide previous observations
immediately; claiming a changed placement resets the cached observation.
Tenant-scoped batch reads join those same pins. Deleting the catalog cascades
health cleanup. Rolling back the migration discards only monitoring state.

The API and CLI show enabled/status/freshness, provider and compute state,
last attempt, last successful metadata read, and a closed error code. `fresh`
refers to the latest attempt, including failed requests. `last_success_at`
means a valid metadata response was received, even if that response describes
a degraded resource; use `status` to assess health. Future-dated timestamps
are stale. With no observation, customer health is unknown; the aggregate
counts classify an unobserved database older than the freshness window as
stale so monitoring cannot silently skip it. Non-ready databases are not polled.
Health is optional on mutation responses and present on GET/list. Reads never
call the provider and retain lifecycle behavior during outages.

Metrics use closed status/outcome labels without tenant or resource identity.
Every replica reports the whole ready catalog; alerts aggregate with `max`,
not a sum. Enabled collectors alert on degraded resources, stale observations,
and stopped sweep heartbeats. A sweep with recorded provider failures still
advances its heartbeat; a catalog/store failure does not.

Validation includes native PostgreSQL contention, lease recovery, deletion and
placement fences, tenant isolation, cancellation, outage/backoff/recovery,
read-only restored Neon observation, suspension, invalid metadata/spec drift,
API/CLI safe projection, SDK generation, and Prometheus rule fixtures. Live
provider qualification remains a separate staging rollout requirement.
