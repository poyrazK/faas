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
| `not_found` (404) | The requested resource is missing or not visible to this account | Check the URL and account, then retry. |

Never paste secrets into an error report. Request IDs and deployment IDs are
safe correlation handles for support and `gregale debug`.
