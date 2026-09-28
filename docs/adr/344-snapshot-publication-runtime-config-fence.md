# ADR-344 · Fence snapshot publication against runtime configuration changes

- **Status:** accepted
- **Date:** 2026-09-28
- **Decision:** `snapshot_written` carries the source instance ID and its
  `started_at` at capture for both init and warm tiers. imaged validates the
  instance's app and deployment, then checks the captured start time against
  the app's latest runtime configuration stamp
  in the same transaction that inserts the snapshot row. An older source is
  acknowledged without activating the deployment. A notification without a
  source ID is accepted only while the app has no configuration stamp.
- **Serialization:** The publication transaction locks the app row before
  reading the stamp. A trigger on `app_runtime_config_changes` takes the same
  lock for every insert or update, including direct managed credential SQL.
  Publication first means the following invalidation sees its row; stamp
  first means publication rejects the older source. Normal stamps use the
  database wall clock at statement execution rather than transaction start.
- **Recovery:** A parked prime instance older than the stamp cannot suppress
  the bounded snapshot prime recovery sweep. The sweep retries after its
  existing grace period. A stale immutable capture is deleted only when no
  live snapshot row points at its key. A locally discarded capture closes the
  timeline with `wake.park_failed` reason `runtime_config_changed`.

This closes the durable notification delay between schedd's local freshness
check and imaged's row insert. Both tiers use the same publication contract.
The existing source check in schedd remains an early exit that avoids sending
known stale captures through the notification path.
