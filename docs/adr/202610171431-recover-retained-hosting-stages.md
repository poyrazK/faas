# Recover an exact retained frontend staging release

Date: 2026-10-17

Status: accepted for internal implementation; live qualification remains deferred.

Add `sites deploy --resume-release RELEASE_UUID --plan FILE --no-promote` behind
the main hosting and planned-staging workstation flags and independent default-off
`FAAS_FRONTEND_HOSTING_STAGE_RECOVERY_ENABLED`. Require a canonical explicit UUID;
empty/malformed recovery arguments and recovery without a plan must never fall
through to release creation. Keep the traffic-suspension `sites resume` command
unchanged. No new API route, store mutation, runtime flag or migration is needed.

Reuse the existing bounded strict saved-plan reader, captured configured API
origin, credential-free target checks, root-bound frozen build and collection
timeouts. Require the exact reviewed manifest, serving settings, API/site target,
production revision and baseline comparison. Reuse existing operator credentials,
API admission, scope, MFA and configuration/history capability gates. CI/OIDC
credentials retain their existing account-usage restriction.

Find exactly the requested release in owned-site inventory. Require a pending or
ready state, matching manifest fingerprint/payload accounting, no first-promotion
marker and no selection as the active release. Reject missing, foreign, duplicate,
retiring, deleted, active or previously promoted records. Recovery must never
guess a release, allocate a replacement or fall back to ordinary deployment.
Optional supplied provenance must exactly match retained immutable labels; omitted
labels remain unchanged and unverified.

The planner's prospective storage/pending/site-release quota blockers describe a
new release and do not apply to recovering an existing admitted reservation. Keep
fresh account evidence and all comparison checks; ignore only those three known
prospective blockers in the private recovery comparison. Retain configuration and
unknown future blockers. Do not subtract, resize, refund or create reservations.
Full quotas or legacy over-quota catalog usage do not prevent completion of the
exact retained release. Server lifecycle/admission guards remain authoritative.

For pending releases, request new grants from the existing endpoint, validate the
exact UUID/manifest/upload destination and upload frozen bytes through the existing
credential-free client. Reupload all declared files; there is no object-existence
probe, partial-transfer checkpoint or file-level deduplication. Finalize once and
verify the exact ready release and unchanged site intent. Retention activity is
touched only through existing grant/finalization behavior.

For an already verified release, refresh the release inventory and site again,
revalidate the exact never-published ready record and return staged metadata. Send
no grant, upload or finalization requests. This handles an explicitly retried
finalization whose response was lost. Include `resumed: true` in successful staged
JSON and preserve the reviewed `expected_revision`.

Never promote, create CI identities/previews, retire resources, initiate cleanup or
automatically retry mutations. Uncertain responses leave the same UUID for manual
inspection and explicit retry; a failed creation whose UUID is unknown still needs
inventory inspection first. Changes can race all independent reads. Server grants
and finalization recheck lifecycle flags/state; late signed uploads may finish
after retirement and remain subject to existing grace/GC rules. Read observations
are not locks, serving-health evidence or promotion authorization. A production
intent change still requires a new review; do not silently replace the revision.
