# ADR-1474: Dashboard tracker sync controls

Date: 2026-10-14

Status: Accepted

## Context

Tracker lifecycle sync and native Linear connections are available through the API
and CLI. The Issues dashboard only exposes references, leaving owners without a
visible policy outcome or a review/recovery flow.

## Decision

Extend each linked reference with its stored automatic policy, confirmed source
and provider lifecycles, pending state, check times and actionable recovery text.
Page loads read Gregale metadata only; an owner explicitly requests a provider
preview through a POST protected by the existing Issues CSRF token, authenticated
session, deploy-write scope and MFA.

Offer owner-only Linear connect/replace/disconnect forms. Password input is never
prefilled, echoed into a preview or placed in a redirect. Reuse the API connection
handler for verified sealed credentials, revisions and audit events.

The preview shows both lifecycles and retains the credential-free native snapshot
in an HTML-escaped hidden input. Aligned snapshots permit explicit automatic
opt-in, requiring both workflow UUIDs for Linear. Unaligned snapshots offer manual
outbound alignment only when automatic delegation is disabled. Check the current
policy revision and disabled state before a manual push. Reuse native API handlers
for scope, workflow and provider-snapshot validation, and the automatic policy
handler for aligned opt-in and revision checks. Successful form mutations redirect
to the issue; API problems pass through on failure.

Disable is available directly from stored policy metadata, without contacting the
provider. Shared readers see outcomes but no provider-management controls.

## Limits

Workflow UUIDs are entered explicitly; workflow discovery and a named picker are
separate work. Dashboard recovery is outbound only; pulling a resolution with a
fixing deployment remains available through explicit CLI sync. Provider and
Gregale do not share an atomic transaction. An already-dispatched write cannot be
recalled, and a competing writer may race after the last observation.

## Validation

Render tests cover owner-only controls, pending-outcome guidance, HTML escaping
and enable availability only for aligned lifecycles. PostgreSQL dashboard tests
use authenticated sessions and mocked Linear operations to cover CSRF rejection,
metadata-only page loads, connect/disconnect, stale connection and policy forms,
manual alignment, stale native previews, refusal to push while delegated,
offline disable and cross-app reference refusal.


Named discovery and selectors are subsequently implemented in
[ADR-1475](1475-named-linear-workflow-picker.md). The connection, authorization,
policy and native-snapshot guards described here remain in use.
