# ADR-202610171147: Read-only frontend deployment activity

Date: 2026-10-17
Status: Accepted for the internal default-off pilot

## Context

Guided setup generates a workflow for an existing trust. Native CI stores stable
run identities, but inspecting one requires copying its UUID from Action output.
Operators need a bounded discovery view connected to diagnostics and release
review. Live GitHub/Cloudflare qualification remains deferred.

## Decision

Add operator-only `GET /v1/internal/hosting/sites/{site}/runs`, with one optional
opaque `before` cursor. Independently default-off runtime
`frontend_hosting_activity_enabled` is seeded by
`FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED`. CLI and dashboard require separate
workstation/build opt-ins. Main hosting, configured providers, operator allowlist,
MFA, read scopes, ownership and automation availability remain prerequisites.
Site CI credentials retain their original endpoint allowlist and cannot enumerate
runs, even with both stage and preview operations. Terraform leaves admission off.

A new optional narrow state reader projects retained run metadata and matching
release/preview evidence. Postgres uses a single SQLC statement with left joins;
MemStore observes records under one lock and returns detached values. Neither
loads release manifests or returns fingerprints, hostnames, origin IDs, credentials
or raw provider errors through the activity API. Source labels come only from a
retained release and are returned only while the existing history API flag is on.
They remain caller-provided and unverified. Cleaned releases have no invented
source snapshot. Preview reads disabled by configuration classify as unknown,
never removed. Automation disabled/unconfigured makes listing unavailable.

Return at most 25 entries, newest first, using `(created_at DESC, id DESC)` keyset
pagination. The cursor is bounded canonical URL-safe base64 of a UTC RFC3339Nano
time and canonical nonzero UUID. It grants no authority and need not identify an
existing record; every request rechecks site ownership. Read one extra row for the
next cursor. The existing 1,000-run site quota bounds sorting without a new index
or migration. Listing does not delete identities, free that quota, extend expiry,
retry, allocate, verify, promote or publish anything. Newer insertions do not shift
an older page; returning to newest is explicit.

Reuse the existing diagnostic classifier for site/run/release/preview checks.
Aggregate those observations into `recorded`, `pending`, `unknown` or `blocked`.
Exclude production publication: an unrelated production release says nothing about
a CI preview. `recorded` means the selected stored checks have no pending/unknown/
blocked classification. It does not assert GitHub completion or serving health.
The response always declares `serving_health: not_checked`. Jobs rejected or failed
before admission have no ledger entry and do not appear. The projection observes
one snapshot, not a persistent execution status. Re-read site intent and feature
availability after collecting; detected changes abort with conflict or disabled.
Storage errors abort, without a partial or fabricated empty report. All responses
use no-store.

CLI `sites runs --site ... [--before ...]` reads exactly one selected page using
existing operator authentication, a deadline and a no-redirect client. It never
chooses a latest run, traverses all pages or substitutes another cursor silently.
Dashboard pagination unmounts/cancels stale reads and hides failed or mismatched
responses. Each row can select its exact original run in independently flagged
diagnostics. Repeated selection resets manually edited diagnostics. Release review
uses the existing current-site comparison and revision check; publication remains
a separate explicit operator action. The existing history build flag also governs
source detail rendering.

## Validation

Memory/Postgres tests cover page boundaries, UUID tie-breaking, ownership, detached
source values and identities retained after release cleanup. Classifier tests
cover pending, recorded, expired, revoked, removed, disabled-preview and provider
error cases, source redaction and exclusion of unrelated production evidence.
Service/server/middleware/CLI tests cover flags, scoped reads, invalid cursors,
read errors, hot disable and CI credential denial. Browser tests exercise exact
run diagnostics, release review without publication, pagination, stale request
cancellation, failed-read hiding and API/build-off behavior. Provider/GitHub live
qualification is deliberately not asserted by this milestone.
