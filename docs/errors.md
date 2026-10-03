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
| `app_startup_timeout`, `app_runtime_oom` | Readiness or memory budget was exceeded | Run `gregale doctor --app APP` for recorded runtime evidence, or `gregale doctor` for local source checks. |
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

## Diagnose a deployed app

Run `gregale doctor --app my-api` after signing in. Add `--json` for a
machine-readable report and `--strict` to fail on advisory warnings. This mode
uses the existing authenticated customer APIs; it does not wake the app or
send a request to its public URL. It cannot be combined with a source path,
`--image`, or registry credentials. Local source and image checks keep their
existing behavior.

The report checks the serving deployment, startup observations, recorded OOM
events, and sampled process memory. A failed newer candidate is a warning
when a previous release still serves traffic. Current failed deployment state
remains relevant until a later release replaces it; its creation timestamp is
reported separately from the time of a runtime observation.

Runtime observations use a five-minute window. Readiness events are matched
to an instance of the selected deployment; a later recorded startup failure
takes precedence over an earlier successful probe. An unclassified boot
failure is reported as a startup failure without asserting that it timed out.
An OOM event for another deployment does not fail the selected release.

Memory evidence is available only when supported guest runners captured process
RSS. It is a sampled process high-water mark, not total VM memory, and does not
measure arbitrary HTTP containers or persistent workers. The advisory warning
starts at 90% of the configured app memory. Route samples must be attributed
entirely to the selected deployment. Missing measurements, mixed revisions,
stale windows, plan restrictions, read failures, and truncated evidence are
reported as `unknown`; a known high memory sample or recorded OOM can still
produce a finding. Quiet or parked apps may have no recent runtime evidence.
Even an `ok` observation does not guarantee present health or complete event
capture.

| Exit code | Meaning in deployed-app mode |
|---|---|
| `0` | All checks have usable observations; warnings are advisory without `--strict`. |
| `1` | A confirmed error, or a warning with `--strict`. Takes precedence over incomplete checks. |
| `2` | Invalid arguments. |
| `3` | Incomplete diagnostics, authentication/API failure, or output failure. |

The CLI makes bounded history reads. If a serving deployment or relevant
runtime observation cannot be found in that history, the check remains
`unknown`. Raw event payloads, persisted logs, and provider error bodies are
not copied into the report; suggested fixes reuse Gregale's error catalog.
