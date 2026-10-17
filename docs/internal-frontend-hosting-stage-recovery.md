# Recover an interrupted frontend staging release

Recovery continues an exact retained release using the original reviewed
[saved plan](internal-frontend-hosting-planned-staging.md). It reuses the existing
storage reservation and never creates a replacement. See
[the decision](adr/202610171431-recover-retained-hosting-stages.md).

## Enable and recover

Use the existing operator API credentials and these independent default-off
workstation flags:

```bash
export FAAS_FRONTEND_HOSTING_ENABLED=1
export FAAS_FRONTEND_HOSTING_PLANNED_STAGING_ENABLED=1
export FAAS_FRONTEND_HOSTING_STAGE_RECOVERY_ENABLED=1

gregale sites deploy --site SITE_UUID --path dist \
  --plan plan.json --resume-release EXACT_RELEASE_UUID --no-promote
```

Use the exact UUID reported by the interrupted staging command. If creation itself
was uncertain and no UUID was confirmed, inspect `sites releases --site SITE_UUID`
first. Recovery does not choose the newest release, infer identity from a matching
name/digest, or create a replacement when an ID cannot be found.

The plan must remain a complete supported version-1 report for the same configured
API/site. Keep local files, `--spa` choice and release configuration unchanged.
Configuration still needs the existing workstation and API configuration flags.
The planning-generation flag and local-preview flag need not be enabled to consume
the saved report. Existing main API hosting, operator, scopes and MFA checks apply.
CI/OIDC credentials remain insufficient for the account usage reads.

`--resume-release` requires both `--plan` and `--no-promote`. Empty/invalid UUIDs,
combined promotion and publication waits are rejected. The separate `sites resume`
command continues to mean resuming suspended site traffic.

## What recovery does

The CLI freezes and verifies local bytes before API calls, then refreshes site,
release, capability and account evidence. Target, production revision, selected
baseline, file/settings comparison and retained content must match the reviewed
plan. A changed production intent stops recovery for inspection and a new review;
the command never substitutes a newer revision.

Pending releases receive fresh upload grants, which also handle expired grants
from the previous attempt. Every declared file may be uploaded again, including
already transferred files. Finalization verifies exact sizes/digests through the
existing server workflow. Recovery reuses the same release UUID, storage bytes and
pending slot; it requests no new release reservation. Full storage, pending-upload
or site-release quotas, including legacy over-quota catalog usage, do not create a
new admission requirement for the retained release. Existing lifecycle and feature
guards still apply; configuration and unknown blockers remain blocking.

An already verified, never-published release is refreshed and returned through
GET requests only. No upload grants, object writes or finalization are repeated.
This is the path after a finalization succeeded but its API response was lost.

Missing, foreign, retiring, deleted, active or previously promoted releases are
rejected. An immutable manifest mismatch also stops recovery. The command preserves
retained provenance; if source-label flags are supplied again, their values must
match and their existing history opt-ins must remain enabled. Source labels remain
unverified and outside the build fingerprint.

Success returns the normal staged JSON with `resumed: true`, the verified `release`
and original `expected_revision`. Recovering never makes a release public. Inspect
and compare the actual retained release against current production before an
explicit promotion:

```bash
gregale sites diff --site SITE_UUID --release EXACT_RELEASE_UUID
gregale sites promote --site SITE_UUID --release EXACT_RELEASE_UUID --expected-revision REVIEWED_REVISION
```

## Failures and concurrency

No mutation is retried automatically. A failed recovery retains the same UUID for
inspection and explicit retry. There is no replacement release, automatic
retirement, cleanup or rollback. Existing reservation/grace/retention rules remain
in force. Errors keep the existing CLI exit codes and emit no successful staged
report. Disabling recovery stops new workstation recovery attempts.

Independent observations cannot lock production or prevent every retirement race.
The backend rechecks release state and feature guards for grants/finalization;
already issued grants can still upload isolated staging bytes until expiry.
Never interpret a recovered result as live serving health or approval to publish.
This workflow requires no direct Cloudflare credentials or live pilot validation.
