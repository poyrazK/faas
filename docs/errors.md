# Errors

Gregale errors use RFC 7807 JSON. The `code` is stable for automation;
`detail` names the observed value or failing stage; `docs_url` points to the
relevant recovery guide. CLI output preserves those fields and the next
command when one is known.

| Code family | Meaning | Next step |
|---|---|---|
| `plan_*` | The current plan does not include a feature or quota | Check [plans](plans.md) and upgrade or reduce the request. |
| `app-not-listening`, `app-loopback-bound` | The process did not expose a reachable listener | Bind `0.0.0.0:$PORT`; rerun `gregale deploy --dry-run`. |
| `dep-install-failed` | Dependencies could not be installed | Pin the lockfile and inspect `gregale logs APP`. |
| `app-startup-timeout`, `app-runtime-oom` | Readiness or memory budget was exceeded | Run `gregale doctor` and select a compatible profile. |
| `validation_failed` | A request or manifest is malformed | Correct the named field; no write is committed. |
| `capacity` | The platform cannot admit work now | Retry with backoff; the response includes `Retry-After` where applicable. |

Never paste secrets into an error report. Request IDs and deployment IDs are
safe correlation handles for support and `gregale debug`.
