# Durable entity health and recovery

This local preview adds observations and alert rules; it has not been qualified
on a native host or live bucket. See [ADR-847](../adr/847-durable-entity-operational-health.md)
and [the recovery contract](../adr/846-durable-entity-exhausted-work-recovery.md).

## Enablement and scan cost

Set `FAAS_DURABLE_ENTITY_HEALTH_ENABLED=1` only with the existing durable entity
preview configuration, private bucket, backend fingerprint and explicit app
allowlist. Health enablement defaults off. The startup discovery check needs
private delimiter LIST; the read worker needs GET. Existing engine startup
conditional-write probes still apply. No additional DELETE authority is needed.
Alarm and outbox processing retain their own enablement flags. Before the first
rotation completes, the success timestamp is zero and a staleness warning can
fire after its five-minute hold; this also signals incomplete startup observations.

The worker observes up to eight prefixes per poll, waits 30 seconds between
polls, and has a 20-second scan deadline and two-second entity read deadlines.
These bounds are in `pkg/api/limits.go`. It reads one manifest per prefix and a
snapshot for enabled committed entities. Each replica scans independently.
For large buckets, a rotation can exceed the initial 15-minute freshness warning;
review scan duration and provider/read capacity before changing limits or alerts.

## Read observations

| Metric suffix after `apid_durable_entity_health_` | Meaning |
| --- | --- |
| `enabled` | Opt-in worker is running on this replica. |
| `poll_success` | Current discovery is healthy; zero on failed/partial observations. |
| `last_success_timestamp_seconds` | Last completed successful rotation; zero until one completes. |
| `observed_entities` | Allowlisted entity observations in that rotation. |
| `pending_work{target}` | Alarm deadlines or pending outbox messages observed. |
| `exhausted_entities{target}` | Entities with exhausted alarm/outbox attempts observed. |
| `oldest_pending_timestamp_seconds{target}` | Saved alarm deadline or oldest known outbox timestamp; zero when none known. |
| `unknown_age_messages` | Pending legacy messages without stored timestamps. |
| `pages_total{outcome}` | Successful, partial or failed scan pages. |

Use `time() - apid_durable_entity_health_oldest_pending_timestamp_seconds` only
when its timestamp is positive and observations are fresh and healthy. Future
alarm deadlines yield negative values; clamp these to zero for display.
Unknown-age messages remain pending even when no oldest timestamp is known.

Observations span a rotation and are not atomic inventory. Concurrent bucket
changes can omit or repeat observations. Do not sum replicas that inspect the
same bucket, treat zero as proof of fleet emptiness, or use these gauges as
billing/dispatch authority. Failed/partial rotations retain historical gauges.
Outbox age ends at transport acknowledgement, not receiver completion.

## Respond to alerts

For scan failure or staleness, check enablement, private listing/read permissions,
provider availability, scan bounds and rotation size. Corrupt or missing committed
snapshots fail closed; never replace them with empty state or delete manifests
to suppress an alert. An unreadable prefix can invalidate a rotation even when
its app cannot be identified as allowlisted.

For aged or exhausted work, identify the entity using the application's known
namespace/key and environment/customer scope. Metrics intentionally contain no
entity labels and do not provide a public fleet entity listing. Use owner
inspection to read the exact current state; resolve handler, destination,
account/customer hold or plan issues before retrying.

Submit recovery with the inspection's exact version, recovery revision and
alarm deadline or outbox head ID. A current owner blocks recovery; a stale
observation returns conflict. After an uncertain response, inspect again before
deciding whether to submit another recovery. Recovery resets retry metadata only;
ensure the appropriate worker is enabled. It does not resend terminal receiver
deliveries. An empty bucket queue does not prove receiver completion.

Monitor recovery engine outcomes with
`apid_durable_entity_operations_total{operation=~"retry_alarm|retry_outbox"}`.
Success means metadata reset; conflict/busy/uncertain/failed have the recovery
contract's meanings. API admission denials are outside these engine counters.
Successful acknowledged recovery also emits the existing best-effort
`durable_entity.retry_rearmed` audit event.

## Qualification handoff

The testing agent should exercise the commit → failed retries → inspection →
recovery → restarted worker → deduplicated handoff flow, preserve original state
and message IDs, and check that health observations clear after acknowledgement.
Include lost bucket/SQL acknowledgements, exhausted alarms, receiver dead letters,
pruned delivery history, partial rotations, legacy timestamps and predecessor
ownership. Run Go/package and SDK checks plus Prometheus rule validation and
alert evaluation. Complete native KVM and private provider qualification before
claiming production readiness. No checks were run from this cloud workspace.
