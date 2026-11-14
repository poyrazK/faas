# Outbound integrations

Call a third-party API from your app without putting its key in your app's
environment. You register the provider once as an integration on your account:
its HTTPS origin, the methods and paths your apps may call, and its
`Authorization` value. Then you bind it to the apps that need it. Inside a bound
app, requests go to Gregale's outbound relay. The relay adds the credential,
enforces the route policy and limits, and forwards the call to the provider. The
key is never visible to your code.

> **Preview.** Outbound integrations are a preview capability on the Hobby, Pro
> and Scale plans, released per account. Ask support to enable them. Behavior
> and limits can change before general availability.

## Create an integration

```sh
curl -X POST https://api.gregale.dev/v1/outbound/integrations \
  -H "Authorization: Bearer $GREGALE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "email",
    "origin": "https://api.resend.com",
    "allowed_methods": ["POST"],
    "allowed_path_prefixes": ["/emails"]
  }'
```

- `origin` must be HTTPS. It must not contain a query or fragment, and it must
  resolve only to public addresses. Private, link-local and metadata addresses
  are refused.
- `allowed_methods` and `allowed_path_prefixes` are the route policy. The relay
  refuses any request outside it.
- `daily_request_limit` is optional. It caps the requests the integration
  admits each UTC day, up to your plan's ceiling (see [Limits](#limits)).
- `request_policy` is optional. Without it, the integration starts with the
  default admission policy (see [Request policy](#request-policy)).

The response is the integration, including its `id`. You use the `id` in every
later call. An account can have 25 enabled integrations. A duplicate name
returns `409`, and the 26th integration returns `429 outbound_integration_limit`.

`GET /v1/outbound/integrations` lists the integrations your account can bind.
`DELETE /v1/outbound/integrations/{id}` removes one.

## Set the credential

Upload the exact `Authorization` header value the provider expects:

```sh
curl -X PUT https://api.gregale.dev/v1/outbound/integrations/$INTEGRATION_ID/credential \
  -H "Authorization: Bearer $GREGALE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"authorization": "Bearer re_..."}'
```

The value must be printable ASCII with no leading or trailing spaces. Gregale
seals it at rest. No API response returns it; the integration only reports
`credential_configured: true`. To rotate the credential, `PUT` a new value.
`DELETE` on the same path removes it, and calls through the integration then
fail until you set a new one.

## Bind it to an app

```sh
curl -X PUT https://api.gregale.dev/v1/apps/my-app/outbound-bindings/$INTEGRATION_ID \
  -H "Authorization: Bearer $GREGALE_TOKEN"
```

A binding gives that app's instances access to the integration and nothing
else. `GET /v1/apps/{slug}/outbound-bindings` lists an app's bindings, and
`DELETE /v1/apps/{slug}/outbound-bindings/{id}` removes one.

To give one app less access than the integration allows, narrow the binding:

```sh
curl -X PATCH https://api.gregale.dev/v1/apps/my-app/outbound-bindings/$INTEGRATION_ID \
  -H "Authorization: Bearer $GREGALE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"allowed_methods": ["POST"], "allowed_path_prefixes": ["/emails/batch"]}'
```

A binding policy can only narrow the integration's methods and paths. A policy
that would widen them returns `400`.

## Call the provider from your app

Gregale sets `GREGALE_OUTBOUND_URL` in your app's environment. Send requests to
`$GREGALE_OUTBOUND_URL/i/{integration id}` followed by the provider path, with
no credential:

```js
const res = await fetch(`${process.env.GREGALE_OUTBOUND_URL}/i/${INTEGRATION_ID}/emails`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ from, to, subject, html }),
});
```

The relay identifies the calling app from its VM, so your app holds no token.
If the app isn't bound to the integration, or the request falls outside the
route policy, the relay refuses the request before it reaches the provider.

## Request policy

Each integration has an admission policy, which you replace as a whole:

```sh
curl -X PUT https://api.gregale.dev/v1/outbound/integrations/$INTEGRATION_ID/request-policy \
  -H "Authorization: Bearer $GREGALE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"request_policy": {
        "rate_per_second": 10, "burst": 20, "max_in_flight": 10,
        "request_timeout_ms": 30000, "max_retries": 1,
        "response_cache_ttl_seconds": 0,
        "circuit_breaker_failure_threshold": 5, "circuit_breaker_open_seconds": 30,
        "retry_budget_per_minute": 60}}'
```

| Field | Meaning |
|---|---|
| `rate_per_second`, `burst` | Token-bucket admission rate for the integration |
| `max_in_flight` | Concurrent upstream requests |
| `request_timeout_ms` | Deadline for one admitted call, retries included |
| `max_retries` | Extra attempts for bodyless `GET`/`HEAD` only (0–2) |
| `response_cache_ttl_seconds` | Opt-in cache for eligible `GET` responses (0 disables; at most 300) |
| `circuit_breaker_failure_threshold`, `circuit_breaker_open_seconds` | Open the integration after this many consecutive transient provider failures, for this many seconds (1–20 and 1–300; both 0 disables) |
| `retry_budget_per_minute` | Cap on extra attempts across all callers (0 leaves only `max_retries`) |

A policy above your plan's ceiling returns `400`; Gregale doesn't silently
clamp a write. If you later move to a smaller plan, the new ceilings apply on
the next request without rewriting your stored policy.

## Limits

| Plan | Requests per day | Rate per second | Burst | In flight | Timeout | Retry budget per minute |
|---|---|---|---|---|---|---|
| Hobby | 1,000,000 | 20 | 100 | 50 | 60 s | 120 |
| Pro | 10,000,000 | 100 | 500 | 250 | 120 s | 600 |
| Scale | 100,000,000 | 500 | 2,000 | 1,000 | 300 s | 3,000 |

Daily request budgets are optional, and you can set them per integration and per
app binding:

```sh
# Integration-wide budget; null removes it.
curl -X PUT .../v1/outbound/integrations/$INTEGRATION_ID/budget \
  -d '{"daily_request_limit": 50000}'
# One app's budget on its binding.
curl -X PUT .../v1/apps/my-app/outbound-bindings/$INTEGRATION_ID/budget \
  -d '{"daily_request_limit": 5000}'
```

`GET /v1/outbound/integrations/{id}/usage` and
`GET /v1/apps/{slug}/outbound-bindings/{id}/usage` return `daily_request_count`,
the effective `daily_request_limit`, the UTC `usage_date` and `resets_at`. A
call counts when the relay admits it, even if the provider later fails.

## Verify a binding

`gregale app binding verify` can probe an integration through the relay, with
the candidate release's own identity. A customer integration opts in by
declaring an endpoint that is safe to call:

```sh
gregale app binding probe-policy set $INTEGRATION_ID --path /domains --method GET --expect-status 200
gregale app binding verify my-app --release v12 --outbound $INTEGRATION_ID
```

The API equivalent is `PUT /v1/outbound/integrations/{id}/probe-policy` with
`{"method": "GET", "path": "/domains", "expected_status": 200}`. Declaring a
probe sends no provider request. `gregale app binding probe-policy set
$INTEGRATION_ID --delete` removes it.

The probe must be `GET` or `HEAD`, use a canonical path without a query that the
integration's route policy allows, and expect a status from 200 to 299. An
outbound binding with no probe policy reports `unsupported` connectivity
coverage. See [Networking](networking.md) for how verification results gate a
release.

## Runs

Runs reach an integration only through an explicit grant, separate from app
bindings:

```sh
curl -X PUT https://api.gregale.dev/v1/outbound/integrations/$INTEGRATION_ID/runs \
  -H "Authorization: Bearer $GREGALE_TOKEN" \
  -d '{"enabled": true}'
```

The integration must have a credential and an allowed route first. Otherwise the
grant returns `409 outbound_runs_binding_ineligible`.

## Errors

Errors use the [standard error format](errors.md).

| Status | Code | Cause |
|---|---|---|
| 400 | `validation_failed` | Invalid origin, route, credential, budget or policy, or a value above the plan ceiling |
| 404 | `not_found` | Unknown integration or binding |
| 409 | `conflict` | An integration with that name already exists |
| 409 | `outbound_runs_binding_ineligible` | Runs grant without a credential or an allowed route |
| 429 | `outbound_integration_limit` | 25 enabled integrations already exist |

The relay answers your app with the same format when it refuses a call:

| Status | Code | Cause |
|---|---|---|
| 403 | `outbound_app_not_attached` | The app is not bound to the integration |
| 403 | `outbound_route_not_allowed` | The method or path is outside the integration's or binding's route policy |
| 404 | `outbound_integration_not_found` | Unknown integration ID |
| 503 | `outbound_integration_disabled` | The integration is disabled |
| 503 | `outbound_provider_credential_unavailable` | No credential is set, or it can't be read |
