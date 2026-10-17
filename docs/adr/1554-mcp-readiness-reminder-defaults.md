# ADR-1554: Versioned readiness reminder defaults

## Status

Accepted — 2026-10-17.

## Context

Operators currently enable bounded reminders separately for each MCP readiness
incident. This is easy to miss during an outage. Rule changes must not silently
change acknowledgements or budgets for incidents already being handled.

## Decision

Store an independently versioned reminder default per readiness webhook rule.
Use compare-and-swap writes under the existing app then rule locks, shared with
incident evaluation. Unconfigured version zero is disabled. Allow either both
zero values or an interval of 60–86400 seconds and 1–10 reminders.

Capture the policy and its version in the attention snapshot when a new incident
opens, even when maintenance suppresses delivery. Existing incidents are never
backfilled. New snapshots record `rule_default`; per-incident configuration records
`incident_override` while retaining the captured version. Legacy snapshots remain
readable without metadata. Defaults versions and attention decision versions are
independent. Changing configuration never resets an incident's lifetime counter.

Expose GET/PUT with read/admin authorization and MFA, dashboard forms protected by
the named alert CSRF token, CLI get/set/disable, and Go/Node/Python SDK contracts.
Retain freshness, acknowledgement, maintenance and per-incident delivery guards.

## Consequences

Operators can opt in once for future incidents and inspect inherited provenance.
Disabling future reminders does not disable reminders on incidents already open;
those require explicit incident actions. Upgrade all readiness evaluators before
configuring defaults because older writers cannot retain inheritance metadata.
The append-only migration leaves old snapshots untouched and blocks rollback once
configuration or captured defaults history exists, including captured version zero.
