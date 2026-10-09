# ADR-855: Durable entity operation observability

Status: local implementation, unqualified.

## Decision

Extend the existing operator metrics with storage-call and admitted engine-operation
latency histograms. Keep a fixed operation vocabulary and omit account, app,
namespace, entity, deployment and request identities from metric labels. Entity
inspection remains the exact-entity diagnostic surface; metrics are aggregates.
No new state, SQL schema, background worker or execution authority is introduced.

`apid_durable_entity_storage_duration_seconds{operation}` measures each attempted
GET, PUT, entity/object listing and deletion, including failures. Optional-capability
rejections are included. Existing `backup_scan` counts describe a scan, not a
single provider call, and have no corresponding storage-call latency sample.

`apid_durable_entity_operation_duration_seconds{operation}` measures invoke, alarm,
outbox, maintenance, retry_alarm, retry_outbox, export, restore and validate_restore.
Timings start at engine entry after API admission and selector resolution. Invoke
includes its guest queue; restore includes private ownership, validation and release;
validation includes queue and execution. Nested samples overlap and must not be
summed. Alarm/outbox samples include selection and admission after worker enablement.
Maintenance measures one step. Histogram observations are emitted when calls finish;
process death can lose a sample. They are not an HTTP latency SLO or billing data.

Restore, validation and export now use existing operation outcome counters.
Rejected validation, obsolete expected versions, missing/corrupt state, cancellation
and deadlines have bounded classifications. Uncertain writes retain their uncertain
classification even when joined with cancellation/deadline errors; telemetry cannot
turn them into definite failures. API deployment-pin conflicts classify as conflict.
A false verdict counts as rejected even though the validation HTTP response is 200.
Receipt replay counts as replay and skips validation. Early admission and preview
failures are outside the execution counters. The existing acknowledged-restore audit
event includes its validator bundle digest; this is best-effort audit history, not
an atomic recovery ledger or new history API.

Add an importable Grafana dashboard and operator/qualification runbooks. Repeated
inventory samples are not fleet storage totals. No new alert thresholds are guessed
without qualified traffic evidence. Enablement, lifetime and scrape access continue
to use existing operator metrics wiring. No tests, builds, live provider checks,
Grafana imports or native acceptance were run in this workspace.
