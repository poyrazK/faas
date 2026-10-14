# ADR-1254: MCP connection health alerts and fleet recovery view

## Context

Durable OAuth recovery retained cleanup intent, but operators needed to inspect
individual roles and retirement records to discover authorization and expiry risks.

## Decision

An administrator-only account health endpoint projects public metadata from live
observer bindings and unresolved retirement records. It includes deleted-app
retirements, app and role attribution, recovery actions and existing control paths.
The issue page is bounded to fifty entries; counts and incident fingerprints use
the whole observed inventory. PostgreSQL reads both sources in one repeatable-read
snapshot. An inventory above 4096 source rows is explicitly incomplete; alert
sampling degrades instead of treating a partial result as proof of recovery.

Four webhook-only gauges cover authorization requirements, renewal failures,
retirement retention within twenty-four hours, and unknown issuance. Existing
alert rules and presets supply webhook credentials, cooldown and durable delivery
claims. Stable issue IDs deduplicate unchanged successfully delivered incidents
across evaluator restarts. Changed issue sets remain subject to normal cooldown;
failed firing deliveries keep ordinary retry behavior.

When a complete observation falls below the rule's threshold, a separate signed
alert.resolved delivery correlates to the original incident ID. It does not advance
last_fired_at or consume firing cooldown. Failed recovery deliveries retry after
cooldown; durable delivery idempotency keys prevent concurrent duplicate claims.
Existing recovery audits and delivery accounting remain in place.

Renewal journals explicitly distinguish recorded responses from unknown issuance.
Legacy journals without this evidence require authorization but are not presented
as proven unknown issuance. No grants, frozen provider bindings, source digests,
lease identifiers or provider error messages enter the health view or alerts.

## Consequences

Operators can enable app presets and inspect the account fleet with gregale mcp
health. Healthy ticks remain quiet, unresolved incidents do not repeat unchanged,
and recovery is visible over the same signed webhook channel. Alerts describe
tracked control-plane state, not a live provider acceptance result. Retained
metadata still follows the existing retention policy. No VM lifecycle changes apply.
