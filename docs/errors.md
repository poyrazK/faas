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
| `app_startup_timeout` | Readiness budget was exceeded | Run `gregale doctor` and select a compatible profile. |
| `app_runtime_oom` | The kernel killed the app for using more memory than its plan allows (the detail carries the peak). A deployment that is starting or rolling out fails and the previous release keeps serving; a live release is not failed and the instance is replaced | Lower the app's peak memory or move to a plan with at least the reported peak plus 8 MB. |
| `app_startup_crash` | The app process exited before it became ready (the detail carries its last output lines) | Fix the error in that output; `gregale logs APP` has the full startup log. |
| `execution_mode_conflict` | The app's startup behavior contradicts its `execution_mode` (a worker bound a port or exited, a job served; a worker with a push queue binding may listen for its deliveries) | Set `execution_mode` to match the app, or change its start command. |
| `validation_failed` | A request or manifest is malformed | Correct the named field; no write is committed. |
| `request_budget_exceeded` (504) | The request ran past its wall-clock budget (30 s for apps by default) before the app sent response headers; `limit` and `observed` are in milliseconds | Return headers sooner (stream, or accept and process asynchronously with `gregale invoke --async`), or lower the work per request. A `kind=budget` edge rule can set a route budget up to the plan maximum. |
| `capacity_unavailable` | The platform cannot admit work now | Retry with backoff; the response includes `Retry-After` where applicable. |
| `idempotency_key_reused` (422) | The request's `Idempotency-Key` was used in the last 24 hours for a different request (another body, query, or precondition header); nothing ran, and the first request's result is kept ([ADR-20261022204036](adr/20261022204036-idempotency-key-bound-to-request.md)) | Send a new key for a new request. To get the first request's result, resend it unchanged with the same key. |
| `auth_rate_limited` (429) | Too many failed authentication attempts came from this address | Wait for `Retry-After`, then retry with valid credentials. |
| `ip_denied`, `geo_denied` (403) | An app edge rule refused the client's IP address or country | Check the rule with `gregale edge-rules list --app APP` and any list it references with `gregale edge-rule-lists get NAME`. |
| `unauthorized` (401) | The dashboard session or API credential is missing or expired | Sign in again or refresh the API credential, then retry. |
| `oauth_provider_unavailable` (503) | A third-party sign-in or GitHub connection provider is unavailable on this Gregale installation | Use another sign-in method if available, retry later, or contact support to enable the provider. |
| `event_publish_batch_too_large` (413) | Batch publication exceeds its 1 MiB body limit; no items accepted | Reduce the batch size, preserving each event's source and id. |
| `event_publish_not_attempted` (batch item) | The request budget ended before this item was attempted | Retry the item with its original identity and content. |
| `event_storage_capacity_exhausted` (429 or batch item) | A new event exceeds retained storage count or bytes | Use the supplied limit/observed diagnostics; retry after pruning or a plan upgrade. |
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
| `bindings_check_failed` (409) | Candidate binding preflight has blockers | Inspect `bindings_check.blockers`, resolve them and verify the candidate with `gregale bindings verify APP --deployment ID --all`. |
| `bindings_check_changed` (409) | Configuration, evidence or runtime facts changed, or a proof/push-consumer poll expired before promotion | Inspect `bindings_check.blockers` and retry. For `verification_expired`, verify the candidate again. For `queue_consumer_stale`, restore scheduler polling and wait for a healthy poll; the consumer window is thirty seconds independently of probe age. Traffic remains unchanged. |
| `bindings_gate_unavailable` (503) | Binding catalogs cannot enforce the atomic promotion fence | Update the server and apply its migrations; ensure managed PostgreSQL and traffic state share the same database pool. |
| `outbound_probe_unavailable` (503) | The outbound probe policy catalog cannot be read or saved | Apply the server migrations and restore catalog availability before configuring or verifying the binding. |

Never paste secrets into an error report. Request IDs and deployment IDs are
safe correlation handles for support and `gregale debug`.

### Managed binding application acknowledgement blockers

With `--require-application-ack`, bindings checks and promotion reports include:

| Code | Meaning and remedy |
| --- | --- |
| `application_adoption_unknown` | Managed secret metadata, eligible workload roster, reload outcome or application receipt is missing, incomplete, invalid or unsupported. Inspect `application_adoption.targets`, enable acknowledgement support and apply the current binding secrets. Upgrade older servers that omit adoption. |
| `application_adoption_failed` | A resident workload reports a current-version projection/signal failure or failed application acknowledgement. Resolve the workload failure and acknowledge the current secrets. |
| `application_ack_stale` | A resident workload acknowledged an older managed secret delivery version. Apply and acknowledge the current versions. |
| `application_ack_candidate_unobserved` | This binding has no authorized resident workload from the selected candidate. Start the candidate with the appropriate secret grants and wait for current acknowledgements. Serving-deployment receipts do not satisfy this requirement. |

These blockers cannot be waived by `--allow-unsupported`. Changed adoption
facts at the write boundary use the existing `bindings_check_changed` problem
and `promotion_observations_changed` finding; recheck before retrying. Strict
promotion must use the dedicated `promote-with-application-ack` route; a 404
from an older server requires a server upgrade and no fallback traffic write.
