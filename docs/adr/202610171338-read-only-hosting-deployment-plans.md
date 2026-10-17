# Read-only internal hosting deployment plans

Date: 2026-10-17

Status: accepted for internal implementation; live qualification remains deferred.

Add `gregale sites plan --site SITE_UUID --path dist [--spa]` behind the main
hosting CLI flag and independent default-off
`FAAS_FRONTEND_HOSTING_DEPLOYMENT_PLAN_ENABLED`. The command uses existing
operator-only hosting reads under the server's existing admission/MFA/scope
checks. It adds no server route, rollout switch, dashboard surface, credential
scope or provider access. Site-scoped CI credentials remain insufficient for
account usage; do not widen their allowlist for this command.

Validate and hash local prebuilt output using the deployment scanner, including
configuration gates and serialized manifest size, before any API requests.
Collect capabilities, the owned site, its retained releases and account usage,
then read the site again. Fail without a partial report on failed reads, changed
site evidence, mismatched account/site identity or limits, missing/unverified
active baseline, invalid release inventory or overflowing accounting. Bound the
collection with a timeout in pkg/api/limits.go and the existing SDK response
limits. Use the redirect-rejecting, completion-cache-free hosting client and an
HTTPS operator API origin (loopback HTTP remains available for portable tests).

Compare the local manifest with the verified active release using the same
manifest comparison as an uploaded release review. A site with no selected
release has an empty baseline. Include file metadata changes, sorted paths,
before/after SPA and release configuration, payload size, file count, serialized
manifest size and manifest fingerprint. Preserve the existing release-review
JSON fields while extracting their shared manifest-diff representation.

Plan one new release, even for identical bytes: no deduplication or automatic
reuse. Project the full existing `2 × payload + maximum manifest bytes` storage
reservation, one account pending-upload slot and one retained site release slot.
Retiring releases count until catalog cleanup; never credit prospective cleanup
or release the old production reservation. Include every observed quota blocker
and a server-disabled configuration blocker. Suspension is a warning because
admission can still succeed and deployment does not resume traffic.

These independently observed values are advisory, not an atomic snapshot or a
capacity reservation. Even a stable site revision cannot detect every concurrent
release/account mutation. The server remains authoritative for admission and
promotion CAS. The plan does not bind or authorize a subsequent deployment,
guarantee its success, probe serving health, or check DNS/Access/CDN propagation.
The local build can also change after planning; staged release review remains
the authoritative comparison of uploaded immutable content.

Emit JSON and exit 0 for a complete plan within observed limits, JSON and exit 2
for a complete plan with observed blockers, and no report on invalid/unavailable
inputs or evidence. Errors preserve the CLI's existing validation, authentication,
authorization, conflict, missing-resource, platform and interruption exit codes.
Scripts must parse a complete report and its state, since exit 2 without a report
also denotes authentication failure. Never create releases, run identities,
grants, previews, uploads, cleanup or promotions, and never repeat a mutation.
