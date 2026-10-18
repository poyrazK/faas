# Plan an internal frontend deployment before uploading

`sites plan` compares a local prebuilt directory with the currently selected
production release and estimates the capacity needed for one new release. It
creates no remote resources. See
[the deployment-plan decision](adr/202610171338-read-only-hosting-deployment-plans.md).

## Enable on an operator workstation

Use an existing operator credential and an HTTPS Gregale API origin. The API's
existing internal hosting flag, operator admission, read scope and MFA checks
still apply. This CLI-only feature has its own default-off opt-in:

```bash
export FAAS_FRONTEND_HOSTING_ENABLED=1
export FAAS_FRONTEND_HOSTING_DEPLOYMENT_PLAN_ENABLED=1
gregale sites plan --site SITE_UUID --path dist
# Match the SPA setting you intend to deploy:
gregale sites plan --site SITE_UUID --path dist --spa
```

Output containing `gregale-hosting.json` also requires the existing workstation
configuration opt-in, `FAAS_FRONTEND_HOSTING_CONFIG_ENABLED=1`. If the server
configuration flag is disabled, the plan reports `config_disabled`. It does not
enable that flag. No Cloudflare credentials or live provider qualification are
needed to use this command against a configured Gregale API.

Site automation/OIDC credentials cannot read account usage and are insufficient
for this planner. Do not put an operator token in an untrusted PR workflow. JSON
output is usable in operator-controlled CI with the same existing authorization
requirements; this feature adds no new credential scope or permissions.

## Read the JSON report

- `version`, `api_origin`: saved format version and normalized API target. These
  bind the report to the operator's configured API for optional planned staging.
- `site_id`, `base_release_id`, `expected_revision`: the production intent used
  for comparison. The CLI reads the site again and refuses a changed observation.
- `changes`: sorted added/changed/removed files, before/after sizes and metadata,
  and SPA, redirect, header and custom-404 configuration changes. MIME changes
  count even when file bytes are identical. A site with no selected release has
  an empty baseline. Removed paths are absent from the new build; production's
  retained-release fallback may still serve old assets. Planning does not purge
  retained artifacts or CDN caches.
- `file_count`, `payload_bytes`, `manifest_bytes`, `manifest_fingerprint`: the
  validated local build. The fingerprint covers its manifest, including serving
  settings; it is not an authorization token and does not lock local files.
- `additional_reserved_bytes`: the existing full storage reservation for a new
  release, `2 × payload_bytes + 512 KiB`. Identical output still plans a new release.
- `quotas`: `current`, `additional`, `projected`, `limit` and `blocked` for
  `storage_bytes`, `pending_uploads` and `releases_per_site`.
- `blockers`: every exceeded quota plus `config_disabled` when applicable.
  Retiring rows and their bytes count until cleanup finishes. No prospective
  cleanup savings are subtracted. Account capacity includes other sites.
- `started_at`, `observed_at`, `usage_observed_at`, `advisory`, `warnings`:
  observation timing and interpretation. A suspended site is called out; deploying
  does not resume traffic.

When a complete plan is produced, its state determines the exit code:

| Exit | Meaning | JSON on stdout |
|---|---|---|
| 0 | Complete plan within observed limits | Yes |
| 2 | Complete plan with observed blockers | Yes |
| Nonzero, without a plan | Invalid build/options, failed reads or unreliable evidence | No |

Errors retain the CLI's existing exit codes: 1 for validation, 2 for authentication,
3 for platform/network failure, 4 for not found, 5 for conflict, 6 for forbidden
and 130 for interruption. In particular, exit 2 without a report is an
authentication error, not a blocked plan. Consumers must check for a complete
JSON report and inspect its `state`; do not interpret an exit code alone as a
successful observation. An output-write failure may leave a truncated stream.
API failures never become zero usage or a partial successful plan. Collection is
bounded to 15 seconds and the existing SDK response size limits; unusually large
retained inventories may require inspection through the API instead of a plan.

## Deploy after reviewing

The report is advisory. Independent reads are not an atomic snapshot, even when
the site revision is stable: another deployment or cleanup can change account
capacity or release inventory. The server rechecks admission when creating a
release and uses revision CAS for promotion. A successful plan does not reserve
capacity or guarantee deployment success.

After reviewing and keeping the local output/settings unchanged, use the existing
[stage, review and publish workflow](internal-frontend-hosting-releases.md):

```bash
gregale sites deploy --site SITE_UUID --path dist --spa --no-promote
gregale sites diff --site SITE_UUID --release RELEASE_UUID
gregale sites promote --site SITE_UUID --release RELEASE_UUID --expected-revision REVIEWED_REVISION
```

The uploaded immutable release review must supply the promotion revision. The
local plan is not a deployment authorization token. The independently gated
[saved-plan staging workflow](internal-frontend-hosting-planned-staging.md) can
explicitly consume it through `sites deploy --plan FILE --no-promote`, checking
the reviewed build and target while refreshing capacity. Ordinary deployment
does not automatically consume saved reports. Use [local serving preview](internal-frontend-hosting-local-preview.md)
to inspect build behavior before upload. Neither local preview nor planning
establishes live serving health, DNS/TLS/Access behavior or CDN propagation.
