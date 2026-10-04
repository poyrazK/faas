# Errors

Gregale errors use RFC 9457 JSON. The `code` is stable for automation;
`detail` names the observed value or failing stage; `docs_url` points to the
relevant recovery guide. CLI output preserves those fields and the next
command when one is known. `type` defaults to `about:blank` when no specific
problem URI is supplied. `instance` identifies the specific occurrence when
the caller supplies an occurrence URI.

| Code family | Meaning | Next step |
|---|---|---|
| `plan_*` | The current plan does not include a feature or quota | Check [plans](plans.md) and upgrade or reduce the request. |
| `app_not_listening`, `app_loopback_bound` | The process did not expose a reachable listener | Bind `0.0.0.0:$PORT`; rerun `gregale deploy --dry-run`. |
| `dep_install_failed` | Dependencies could not be installed | Pin the lockfile and inspect `gregale logs APP`. |
| `app_startup_timeout`, `app_runtime_oom` | Readiness or memory budget was exceeded | Run `gregale doctor` and select a compatible profile. |
| `validation_failed` | A request or manifest is malformed | Correct the named field; no write is committed. |
| `capacity_unavailable` | The platform cannot admit work now | Retry with backoff; the response includes `Retry-After` where applicable. |
| `auth_rate_limited` (429) | Too many failed authentication attempts came from this address | Wait for `Retry-After`, then retry with valid credentials. |
| `unauthorized` (401) | The dashboard session or API credential is missing or expired | Sign in again or refresh the API credential, then retry. |
| `oauth_provider_unavailable` (503) | A third-party sign-in or GitHub connection provider is unavailable on this Gregale installation | Use another sign-in method if available, retry later, or contact support to enable the provider. |
| `event_stream_unavailable` (SSE) | Gregale could not open the account event stream | Reconnect in a moment; if it persists, check Gregale status. |
| `bad_request` (400) | A dashboard action is malformed or incomplete | Return to the form, check the requested fields, and retry. |
| `transport_error` (503, CLI) | The CLI could not reach Gregale's API | Check connectivity and the configured API URL, then retry. |
| `bad_gateway` (502) | Gregale could not complete a connection to the app | Retry the request; if it continues, check app logs. |
| `app_unavailable` (503) | The gateway could not reach the selected app instance | Retry after the supplied delay; inspect app logs if the app remains unavailable. |
| `app_health_unavailable` (503) | The app's last known wake did not leave a live instance | Retry after the supplied delay; inspect app logs if the app remains unavailable. |
| `realtime_unavailable` (503) | Gregale cannot currently reach the managed realtime service | Retry after the supplied delay. |
| `app_logs_unavailable`, `log_archive_unconfigured` (503) | Live or archived logs could not be retrieved | Retry shortly; use live logs when archived logs are unavailable. |
| `log_archive_invalid_query` (400) | An archived-log request is missing or has malformed filters | Provide an instance and a date in `YYYY-MM-DD` format. |
| `log_archive_retention_exceeded` (403) | The requested log date is outside the plan's retention window | Choose a more recent date or review plans with longer retention. |
| `verification_link_invalid` (410) | A verification link is malformed, expired, or already used | Request a new verification email and open its latest link. |
| `compute_region_unknown` (422) | The selected compute region is not registered | Choose an identifier from `gregale regions list`. |
| `compute_region_admission_paused` (503) | The region is not accepting new apps | Retry after the operator resumes admission; existing apps retain their region. |
| `compute_region_unavailable` (503) | No eligible compute node is available in the selected region | Inspect `gregale regions list` and retry when the region recovers. |
| `compute_region_capacity_exhausted` (503) | Regional nodes cannot admit the requested resources | Retry later or reduce resources; execution stays inside the selected region. |
| `not_found` (404) | The requested resource is missing or not visible to this account | Check the URL and account, then retry. |

Never paste secrets into an error report. Request IDs and deployment IDs are
safe correlation handles for support and `gregale debug`.

Regional image release retries return `regional_image_release_conflict` (409) when
one release UUID is reused for another image or ordering. Retain the original UUID for matching
retries and choose a new one for a new deployment. `regional_image_release_expired`
(410) means the originally accepted deployment was collected; its receipt remains
and that UUID cannot be executed again. Read the receipt with GET, then choose a
new release UUID if another deployment is intended. See
[durable regional image acceptance](ops/global-app-directory.md#durable-image-release-acceptance).

`regional_image_release_order_conflict` (409) rejects a first submission whose
global generation is not greater than the region's accepted generation, or whose
stream identity or owner differs. Reconcile the current global release instead
of resubmitting an older one with an invented larger generation. Matching receipt
retries retain their original image, global app UUID and generation.

Global release allocation returns `global_image_release_no_targets` (409) when
no regional app is selected and `global_image_release_limit` (409) when the
32-target or exact-JSON generation bound is reached. A changed image for an
existing global release UUID returns `conflict` (409). Regional failures appear
as closed journal diagnostics; retry the same UUID/image with keys for pending
regions. Keys and remote error bodies are never included in those diagnostics.

`regional_release_grant_limit` (409): the active, unexpired regional grant count
exceeds the per-app protocol bound. Includes `limit`, `observed` and `docs_url`.

Directory regional consent returns `global_delegation_limit` (409) when the next
live journal would exceed 1024 delegations for one global app, including pending
revocations. It includes `limit`, `observed`, and
`docs_url=https://gregale.dev/docs/global-app-directory#regional-delegation`.
Wait for revocation or expiry before issuing more consent. Missing sealing
identities or temporary regional authentication backend failures return 503;
these failures do not prove that a delegation has been revoked.
