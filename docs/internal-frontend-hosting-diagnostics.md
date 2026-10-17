# Internal frontend deployment diagnostics

Diagnostics explains stored site and CI deployment evidence without changing
resources. The diagnostic API does not contact GitHub, Cloudflare, DNS, R2 or
the hosted URL to check serving health. A publication acknowledgment means the provider accepted
the recorded routing generation; edge refresh and Access sign-in still apply.

## Flags and access

All opt-ins default off. Keep normal internal hosting providers, main feature
flag, active account, operator allowlist and existing MFA/scope controls.
Enable API runtime `frontend_hosting_diagnostics_enabled`, optionally seeded by
`FAAS_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED=1`. Upgrade API servers before enabling;
no migration is needed. Provider Terraform emits `0` for this flag.

The dashboard additionally requires a build with main hosting enabled and
`NEXT_PUBLIC_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED=true`. The **Deployment
diagnostics** panel appears in site details when the API capability is enabled.
It defaults to the production site. Paste an original CI run UUID from the
Action outputs to inspect that exact intent. It does not choose a latest run,
list all run IDs, automatically retry or change resources. Failed refreshes hide
old recommendations. Selecting a different run aborts the old report request.

API: `GET /v1/internal/hosting/sites/SITE_UUID/diagnostics`, optionally
`?run_id=ORIGINAL_RUN_UUID`. Unknown, duplicated, malformed or empty query values
are rejected. Responses use `Cache-Control: no-store`. A CI site credential needs
both stage and preview operations and is confined to its own site. It receives
no additional account, credential, trust or mutation access.

## CLI

```sh
export FAAS_FRONTEND_HOSTING_ENABLED=1
export FAAS_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED=1
gregale sites diagnose --site SITE_UUID
gregale sites diagnose --site SITE_UUID --run-id ORIGINAL_RUN_UUID
gregale sites diagnose --site SITE_UUID --run-id ORIGINAL_RUN_UUID --path dist
```

The command emits JSON; the CLI's global `--json` option is also supported. Exit
0 means it produced the report, including reports with blockers. It does not mean
the deployment is healthy, published or safe to retry. Authentication, disabled
features, malformed options and read failures return nonzero without a partial
report. With an existing bearer, the only hosting API operations are GET capabilities and GET diagnostics;
there are no upload grants, uploads, finalization or allocation calls.

Use your existing operator credential or a site credential with both operations.
The API origin must use HTTPS; credential-free loopback HTTP origins are accepted
for local tests. Redirects are rejected. In a trusted GitHub job you may instead
supply `--oidc-trust TRUST_UUID` with the existing automation/OIDC workstation
flags and runner identity permission. That authentication mode additionally contacts
GitHub and the existing OIDC exchange to mint or renew short-lived authentication
sessions; it does not change deployment intent. Omit `FAAS_TOKEN` in that mode. See the
[OIDC guide](internal-frontend-hosting-oidc.md).

`--path` needs the original `--run-id`; use `--spa` if it was used originally.
If the output includes `gregale-hosting.json`, explicitly enable the existing
release-config workstation flag. Comparison uses paths, bytes, content types,
SPA/config settings and the original manifest ordering. A match does not compare
source labels or preview TTL; both must remain unchanged. No manifest/file list,
local directory path or source label appears in the report. If the release was
removed there is no manifest to compare, so comparison is unknown.

## Interpreting evidence

Each check has a step, state, stable code, summary and suggested action. States
are `ok`, `pending`, `blocked`, `unknown` or `not_checked`. `ok` describes only
that step. `serving_health` is always `not_checked`.

| Evidence | Meaning and next action |
| --- | --- |
| `release_pending` | Verification has not been acknowledged. Per-file upload completion is not stored. If other checks permit, resume unchanged intent. |
| `preview_not_created` | Preview creation is not recorded. A usable original run may resume; no new ID is needed. |
| `publication_pending`, `origin_unavailable`, `provider_unavailable` | Wait for recorded automatic publication retries, then refresh. No provider retry is triggered by this report. |
| `publication_repair_failed` | An older acknowledgment exists, but the last repair failed. Inspect repair progress; current serving health is unknown. |
| `publication_acknowledged` | Current generation has a timestamped provider acknowledgment. Check edge refresh and normal Access separately. |
| `run_expired`, `preview_revoked`, `preview_removed`, `release_removed`, `release_retiring` | Inspect retained resources. Retrying cannot extend or recreate the old preview/release. |
| `run_not_found` | Confirm site/run IDs against original CI outputs. Absence does not prove a new allocation is appropriate. |
| `automation_unavailable`, `previews_unavailable` | Relevant feature reads were skipped; absence was not inferred. Ask an operator to review configuration. |
| `manifest_changed` | Local output differs. Restore original output/settings; do not reuse the intent for different files. |
| `state_changed`, inconsistent or missing receipt codes | Refresh before deciding how to recover. |

`retry_advice=retry_same_run` is conditional guidance, not authorization or an
automatic retry: keep the original run ID, files, source labels and preview TTL.
The CI endpoint still checks the complete immutable intent and original expiry.
`inspect_before_retry` means blocked or unknown evidence needs review. Never
change a run ID merely to hide an interrupted or ambiguous deployment. New
intent should be an explicit choice after reviewing retained resources.

Observations are assembled from separate reads between `started_at` and
`observed_at`. Detected changes to site/routing intent or the CI marker produce
a refresh requirement. Other concurrent changes can still happen afterward;
refresh before taking an action. Reports contain no token, provider payload,
origin hash, filename, source label or private URL. They are not public health
endpoints or production readiness gates.

Local fake-provider and HTTP/browser tests exercise these behaviors. Live
GitHub/Cloudflare/Access qualification remains deferred.

For discovery without copying run IDs, independently enable the
[deployment activity view](internal-frontend-hosting-activity.md). Its **Inspect run**
button selects the exact original intent here; it never chooses a latest run.
