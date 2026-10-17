# Stage frontend output from a reviewed saved plan

Date: 2026-10-17

Status: accepted for internal implementation; live qualification remains deferred.

Add default-off `gregale sites deploy --plan FILE --no-promote` behind the main
hosting CLI flag and independent `FAAS_FRONTEND_HOSTING_PLANNED_STAGING_ENABLED`.
Require an explicit canonical site UUID matching the saved plan. Keep ordinary
deployment behavior unchanged when `--plan` is absent; an explicitly empty plan
argument is an error. Reject combined deployment/promotion and publication waits
for a saved plan. Promotion remains a separate immutable-release review and CAS.

Extend generated deployment plans with format version 1 and a normalized API
origin. Accept HTTPS origins and loopback HTTP for portable tests; exclude
credentials, paths, queries and fragments. Normalize host case and default ports
without DNS/network discovery. The current configured API and requested site must
match the plan before credentials are loaded or API requests are made. The saved
origin never selects a new client destination. Existing unversioned reports
remain descriptive historical output but must be regenerated for staging.

Read one regular, bounded plan JSON file. Reject symlinks, oversized/truncated
files, unknown fields, duplicate/case-aliased keys, excessive nesting, unsupported
versions, incomplete or blocked reports and inconsistent quota accounting. Put
the 4 MiB saved-file bound, JSON nesting bound, version and ten-minute staging
timeout in pkg/api/limits.go. A saved report is user-controlled review evidence,
not a signed artifact, authorization capability, capacity reservation or serving
health assertion. Source provenance labels remain separately gated and unverified;
they are outside the saved manifest fingerprint.

Bind local reads to an os.Root before the existing deployment scanner runs.
Reread configuration through that root, validate serialized manifest size, then
read declared regular files with per-file bounds and verify exact sizes/digests.
Freeze the payload in CLI memory before API reads. Require its manifest fingerprint
to match the saved plan. The existing 100 MiB payload cap bounds retained bytes,
with additional bounded read and metadata overhead. Later filesystem mutations
cannot alter upload bytes. Do not require or start the local-preview listener.

Use the redirect-rejecting, completion-cache-free operator client. Recollect
capabilities, site, releases and account usage with the existing 15-second read
bound and repeated site observation. Recompute the comparison from frozen output
and current inventory. Require the reviewed target, active baseline, production
revision, fingerprint, file/byte accounting and canonical file/settings comparison
to match. Allow capacity observations to change, but reject new blockers. Require
existing staging/history/configuration capabilities as applicable. Site-scoped
CI credentials remain insufficient; do not widen their allowlist.

Create one release, validate its identity/content/accounting and reject reuse of
an inventoried release. Recheck site intent before requesting grants. Validate
all upload grants against the configured HTTPS upload origin and exact new release
paths, including permitted headers and unique declared paths. Upload frozen bytes
through a fresh client without Gregale credentials/cookies or redirects. Finalize
once, verify the exact created release and manifest, and recheck site intent.
Return the verified staged release with the original reviewed revision only on
success. Never call promotion, CI creation, preview allocation, retirement or
cleanup. Never automatically repeat uncertain mutations or replace the reviewed
revision. Report known retained release IDs so operators can inspect failures.

These are CLI preflight/observation checks, not atomic server-side revision
admission. Account capacity and production intent may change between reads and
release allocation, during upload or after the final read. Existing transactional
server quotas remain authoritative. A detected post-creation change may leave a
pending or ready release and reservation, which must be inspected and reviewed
against current production. Staging cannot publish it; subsequent explicit
promotion still requires the existing authorization and revision CAS. No API
route, database migration, dashboard flag or provider configuration is added.
