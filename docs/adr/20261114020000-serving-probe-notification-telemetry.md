# ADR-20261114020000 · Detect lost direct notification evidence

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-14
- **Extends:** [notification warnings](20261113230001-serving-probe-notification-alerts.md),
  [operator evidence](20261113233000-serving-probe-notification-operator-evidence.md)
  and [direct node lookup](20261114010000-node-probe-notification-lookup.md).

Add a separate normalized `faas_serving_probe_notification:evidence_status`
recording without changing the existing coarse outcome codes. Distinguish
complete outcome/disablement (0), a valid enabled channel without history (1),
missing required fields (2), invalid or inconsistent telemetry (3), and an
unavailable probe source (4). Derive expectations from independent inventory
and bind usable fields to the same fresh, unique discovered node source and
scrape as the existing outcome rules.

Require enabled and attempted fields for each expected channel. An attempted
request or any retained history requires a complete outcome, failure code and
completion clock; a reported success also requires its matching success clock.
Allow absent historical success for a failed submission. Keep valid quiet
channels and clean credential rotation without history distinct from missing
telemetry. Explicit disablement with clean metadata remains complete evidence.

Add `FaasServingProbeNotificationTelemetryDegraded`, held for five minutes,
only for missing/invalid telemetry with an unknown coarse outcome on a usable
source. Keep inventory and source outages attributed to existing warnings.
Retain only stable node/channel and normal routing labels; use the fixed
evidence code as the alert value. A failure warning can clear as telemetry
disappears, and the new warning covers that gap after its hold. Its resolution
does not imply a successful submission or receipt.

Read the new recording and its timestamp in one additional strict query pair.
All fourteen serving-health queries, or the node lookup's two inventory plus
eight filtered queries, retain one pinned time and one shared three-second
deadline. The extra vectors have at most 2,000 channel rows for the full fleet
and two for a node, within existing bounds. No new quota, cardinality label,
probe, notification send, credential read or history write is introduced.

For `reason=recording_unknown`, expose an optional `evidence_reason` with fixed
`no_submission_yet`, `telemetry_missing`, `telemetry_invalid` and
`source_unavailable` reasons only from coherent current
evidence; require a usable original run for the first three. Preserve null
configuration and history. Older rules without the detail retain the coarse
`recording_unknown` reason without detail. Preserve the coarse reason enum so
older clients can ignore the new optional field. Query failures and stale or
contradictory recordings remain explicit unknowns. Known completed outcomes remain independently usable
when the additional diagnostic query is unavailable. Extend OpenAPI/SDK models,
shared CLI validation and deployment evidence without adding qualification gates.

Verify real Prometheus semantics, the five-minute warning hold, failure to lost
telemetry to restored reporting, valid no-history and rotation, disablement,
source and inventory loss, partial/duplicate/foreign/malformed fields, original
clocks, bounded maximum vectors, CLI redaction and SDK round trips. Regenerate
the compiled alert and recording expectation catalogs. Production convergence
remains separate.
