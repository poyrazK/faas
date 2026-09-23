# ADR-213 · Durable unified customer log events

- **Status:** accepted foundation
- **Date:** 2026-09-22
- **Decision:** Add one append-only, tenant-scoped `log_events` ledger for
  customer-queryable runtime, build, deploy, HTTP, network, and DNS events.
  Producers retain ownership of their source systems and publish bounded,
  redacted projections through apid; readers use one app-scoped keyset query.
- **Why:** Gregale currently exposes runtime lines from an in-memory ring,
  archived runtime objects from S3, build/deploy lines from `deployment_logs`,
  and HTTP events from `request_telemetry`. The CLI can present those surfaces
  together, but it cannot provide one durable ordering, cursor, retention
  contract, or cross-source correlation query until the control plane owns a
  canonical customer log record.
- **Consequences:** `log_events` is monthly range-partitioned by `occurred_at`,
  denormalizes `account_id` and `app_id` for IDOR-safe indexed reads, and uses
  `(occurred_at DESC, id DESC)` keyset pagination. Its closed source vocabulary
  is `runtime|build|deploy|http|network|dns`. Structured filter columns cover
  release, instance, request/trace, route, method, status, severity, and stream;
  `fields` is bounded metadata only. Request bodies, headers, credentials,
  environment values, and unrestricted span attributes are forbidden. A
  source event ID plus its original timestamp provides replay idempotency.
  Retention is capped by the existing per-plan log-archive retention setting.
  This foundation PR owns schema and store boundaries only; source adapters,
  retention sweeping, and the public query endpoint land as stacked changes.
- **Rejected alternatives:** A SQL view over `deployment_logs` and
  `request_telemetry` cannot include live/runtime archives, cannot impose one
  retention policy, and preserves request telemetry's aggregate-row semantics.
  Reusing Loki would make a tenant product API depend on the operator logging
  plane and its labels. Keeping only the existing S3 objects gives cheap
  retention but no indexed route/status/request lookup. Replacing each source
  table is also rejected: those tables remain authoritative for their current
  workflows, while `log_events` is a deliberately bounded read projection.

## Ownership and rollout

`apid` remains the only database writer. Runtime, builder, gateway, network,
and DNS producers send typed projections to apid over authenticated internal
transports; they never receive database credentials. Writes must preserve the
source timestamp and a stable source event ID so retries converge on the same
ledger row. Source adapters roll out independently and may dual-write while
their existing customer surfaces remain available.

The first adapter projects accepted gateway HTTP telemetry aggregates inside
`apid`. The existing plan and rate gates still decide which aggregates are
accepted. The gateway's stable `event_id` and the original telemetry timestamp
identify a replay; one transaction commits the telemetry row and its redacted
log projection, and a replay commits neither again. Historical telemetry rows
are not backfilled by this adapter.

`meterd` reconciles the current and next two UTC month partitions on startup
and hourly, relocating overlapping default-partition rows before attachment.
The exclusive-lock reconciliation has a five-second deadline; a larger backlog
remains in the default partition and raises a failure metric rather than
blocking log ingestion indefinitely.
It deletes expired events in bounded batches using each account's log archive
cap (Free 1 day, Hobby 7, Pro 30, Scale 90), then drops whole partitions only
after the 90-day maximum has passed. Missing accounts use the one-day floor.
Coverage, default-partition growth, deletion counts, and failures are exported
as meterd metrics.

The public read path is app-scoped and always predicates both `account_id` and
`app_id`. A cursor contains the fixed query window plus the last
`(occurred_at,id)` tuple. Adding a new source is an additive vocabulary change
that requires an ADR amendment, producer redaction tests, and retention-cost
qualification.

## Payload and cardinality limits

- `message`: 1–16,384 characters after producer-side line fragmentation.
- `route`, `request_id`, `trace_id`, `source_event_id`: bounded indexed text.
- `fields`: a JSON object containing at most 32 KiB of non-secret metadata.
- `occurrences`: positive count; normally 1, greater only for an explicitly
  aggregated source projection.
- No index accepts arbitrary metadata keys or message contents. Full-text
  search requires a separate capacity and privacy decision.

## Cross-references

- ADR-043: per-instance runtime ring and streaming path.
- ADR-127: production debugger and `request_telemetry`.
- ADR-129: deployment observability.
- ADR-179: additive projection precedent for unified ledgers.
