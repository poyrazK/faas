# Durable entity operation visibility

Local and unqualified. Import [the dashboard](../../deploy/grafana/durable-entities.json)
into the operator Grafana using the existing `prometheus` datasource. This work
does not import it into a running service. See [ADR-945](../adr/945-durable-entity-operation-observability.md)
for metric boundaries and [the qualification handoff](FaasDurableEntityQualification.md)
for evidence requirements.

The dashboard compares engine p95 latency with private-storage p95. Sustained
storage latency suggests provider/network/permission investigation. High engine
latency with healthy storage suggests guest queue, app execution, contention or
validation costs. Histogram quantiles need sufficient samples; missing series or
no traffic means unknown. Operations overlap: validation is part of restore.

Busy/conflict outcomes are normal under contention. Inspect the exact entity using
known application selectors; check pending work and owner status before retrying.
For uncertain outcomes, retry the identical stable request and payload. A timeout
is not proof that a write failed. Never change the request identity to bypass an
uncertain restore. Corruption is distinct from missing state; never replace a
corrupt committed snapshot with empty state.

Restore rejection means the validator did not accept the candidate. Check schema
compatibility, deployment pin and registered bundle. A validation success does
not reserve a version or prove a later restore succeeded. Observe restore success
or receipt replay and inspect the committed version. Timeouts/cancellation request
isolated execution cancellation; the scheduler still owns teardown. Replay skips
validation, so the two counters need not match.

Use existing exact-entity inspection for pending/exhausted alarms and outbox work.
[Health and recovery](FaasDurableEntityHealth.md) covers stale scan and retry recovery.
Metrics contain no customer or entity IDs. Storage sample averages and upload rates
are diagnostic observations, not current fleet usage or a cost invoice. Use provider
billing evidence for physical retention, versioning and request prices.

The existing `durable_entity.state_restored` audit event records acknowledged restores,
source/committed versions, replay and validator pins. Audit insertion is best effort;
it can be absent after a committed restore. It does not expose candidate state or
provide a complete entity restore-history API.
