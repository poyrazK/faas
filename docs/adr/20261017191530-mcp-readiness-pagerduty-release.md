# ADR 20261017191530: Opt-in PagerDuty acknowledgement release

## Status

Accepted, 2026-10-17.

## Context

Independent primary/fallback subscriptions can pause readiness reminders. An
operator who unacknowledges in PagerDuty must currently release locally as well.
Automatic release needs known provider ownership, separate destination ordering,
and protection for local decisions and immutable acknowledgement replay.

## Decision

Add a default-off `sync_unacknowledgements` setting to each incoming binding.
Accept signed v3 `incident.unacknowledged` only when enabled, with incident status
`triggered` and the existing service, regional endpoint, dedup key, age, signature,
secret namespace, account/application and captured routing checks. Recheck binding
version and destination under existing app-before-rule locks. Ignore unrelated
provider events; readiness probes continue to own health and recovery.

A fresh effective provider acknowledgement starts tracked ownership when opted
in. Persist at most primary/fallback hold records and inactive timestamp watermarks
in the attention snapshot. Existing active legacy/manual acknowledgements are not
adopted. Secondary acknowledgements independently hold the pause. A hold captures
the provider incident, event, region, service, integration fingerprint, policy
version and release eligibility. Fingerprints remain private. Release clears only
a matching eligible hold; multiple different provider incidents on one destination
make its hold require local release. A known ACK-only hold can become releasable
on a fresh matching acknowledgement after enabling.

Reject events older than a destination watermark or at/before the latest local
decision. Equal timestamps favor acknowledgement. Release before acknowledgement
retains an inactive watermark. Manual acknowledge/release clears tracked holds;
manual reminder configuration preserves holds and advances the local barrier.
Preserve manual expiry and never release an active manual acknowledgement.

Keep acknowledgement deduplication, digests and receipt UUIDs unchanged. Release
receipts use `(rule_id,event_id,delivery_destination)` so a shared event can reach
both subscriptions independently. Cross-destination semantic digest conflicts
remain rejected. All receipts share the existing 128-per-incident bound, checked
after duplicate lookup. Ownership/watermark mutations increment attention CAS
versions; only effective global transitions create canonical attention actions.
All decisions and receipts commit together in memory and PostgreSQL.

Resume reminders only after the last eligible hold clears, using the existing
interval and remaining lifetime budget. Do not reset count, bypass evidence or
maintenance guards, or change incident health. Expose safe active-hold metadata,
settings and event actions through the API, dashboard, CLI and Go/Node/Python SDKs.

## Consequences

Existing clients and subscriptions retain acknowledgement-only behavior. Primary
and fallback release settings and holds are independent; manual actions remain
authoritative. Ambiguous ownership deliberately requires local release.

Migration 20261017181315189 preserves legacy receipt hashes and default-off policy
versions and checks ownership/delivery shapes. Pause configuration and incoming
callbacks during migration and upgrade every apid, evaluator and attention writer
before enabling: older snapshot writers cannot retain these JSON fields and old
receipt readers assume a single row per event ID. Begin a new provider acknowledgement
cycle after upgrading. Rollback refuses enabled flags, any retained ownership or
watermarks, and release audit receipts; it never silently deletes history.

Memory/PostgreSQL lifecycle and restart tests, signed HTTP tests, migration replay,
and public client contracts qualify the local implementation. Real PagerDuty
subscriptions, US/EU routing and operational rollout require live qualification.
