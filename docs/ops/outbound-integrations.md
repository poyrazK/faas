# Request-aware outbound integrations

`outboundd` is an opt-in HTTP gateway for provider APIs. It gives every
instance attached to an integration one shared rate budget and one shared
in-flight limit. Route a private, guest-reachable address to the daemon's
listener; the per-VM `10.0.0.1` tap address is inside each namespace and is not
a host listener.

Run the daemon on the host-side guest gateway address and apply the migration
before starting it:

```sh
systemctl enable --now faas-outboundd
```

The daemon also exposes an operator-only Prometheus endpoint on
`127.0.0.1:9108` by default (override with `metrics_addr`). It publishes
`outbound_admissions_total`, `outbound_rejections_total`,
`outbound_in_flight`, `outbound_upstream_requests_total`, and
`outbound_upstream_latency_seconds`,
`outbound_response_cache_requests_total`,
`outbound_circuit_breaker_events_total`,
`outbound_retry_budget_events_total`, and
`outbound_provider_cooldown_events_total`. Operator integrations use their
configuration-owned IDs; all customer-created integrations share the bounded
`customer_managed` label. Outcomes and rejection reasons also use bounded
vocabularies. The in-flight gauge is per gateway process; the Postgres-backed
admission decision remains the authoritative fleet-wide limit.

The daemon's default upstream transport resolves provider names immediately
before opening a socket, rejects the entire DNS answer if any address is
private, reserved, loopback, link-local, or otherwise not globally reachable,
and connects directly to one of the checked public IPs. TLS still verifies
the configured hostname. Redirects and environment-configured HTTP proxies
are disabled so a redirect or proxy cannot move resolution outside this
check. Private and special-use provider addresses are not supported by the
default gateway transport.

Create `/etc/faas/outboundd.toml` from the Ansible example. Each integration
specifies a UUID, a fixed `https://` origin, attached app UUIDs, a rate/burst,
`max_in_flight`, and optionally `daily_request_limit`. Put the raw Gregale
gateway token in the named environment file
(`/etc/faas/secrets/outboundd/outboundd.env`); only its SHA-256 digest is stored
in Postgres. For integrations without a managed provider credential,
provider authentication remains application-owned: non-`X-Gregale-*` headers
such as `Authorization` are forwarded. Those legacy integrations still need
their gateway token and app ID in each request.

To keep a provider key out of an app, set `provider_authorization_env` on its
integration and put the complete header value (for example, `Bearer sk_...`)
in the same private `outboundd.env` file. `outboundd` reads it at startup and
never stores it in Postgres or passes it to the app. For that integration, the
gateway replaces any app-supplied `Authorization` header before the provider
request. Other integrations retain application-owned provider headers. A
missing or malformed configured value prevents daemon startup; a replica
without the key for a managed integration returns 503 without contacting the
provider. Rotate the key by updating the private environment file and
restarting `outboundd`. The configured gateway token is still required for
the existing database schema but is **not** given to apps or accepted as
authentication for managed integrations.

`app_ids` may be empty for a managed integration. Once the operator has
provisioned it, the account can list available integrations with
`GET /v1/outbound/integrations`, attach one with
`PUT /v1/apps/{slug}/outbound-bindings/{integration_id}`, list an app's
attachments with `GET /v1/apps/{slug}/outbound-bindings`, and remove one with
`DELETE /v1/apps/{slug}/outbound-bindings/{integration_id}`. These API calls
carry no provider credential. `apid` owns the durable customer binding row;
`outboundd` reads it at request time alongside operator `app_ids`, so a bind
or unbind needs no daemon restart. A customer unbind does not remove an
operator attachment. The account must own both app and managed integration.
See [ADR-252](../adr/252-customer-outbound-binding-intent.md).

After binding, an account can narrow that app's HTTP routes with
`PATCH /v1/apps/{slug}/outbound-bindings/{integration_id}`:

```json
{"allowed_methods":["GET"],"allowed_path_prefixes":["/v1/customers"]}
```

Both arrays are required and nonempty. Each method and whole-segment path
prefix must be contained in the integration's configured route ceiling. The
binding initially inherits that ceiling, and a repeated `PUT` does not reset a
narrowed binding. `GET` on the app's bindings reports both the integration
ceiling and the app-specific route policy. The gateway intersects them on
every request, so later policy tightening takes effect without editing the
customer binding. For operator-provisioned integrations, an explicit operator
`app_ids` attachment continues to grant the full operator ceiling even if a
customer binding for the same app is narrower. The PATCH route requires MFA
and deploy-write scope.
See [ADR-254](../adr/254-customer-outbound-binding-route-policy.md).

For a customer-held provider credential on an operator-provisioned integration,
configure the managed integration with
`credential_source = "customer_sealed"` and **omit** `provider_authorization_env`.
Keep the explicit method/path allowlist, workload-identity JWKS, and gateway
token configuration. The account can then `PUT
/v1/outbound/integrations/{integration_id}/credential` with JSON
`{"authorization":"Bearer <provider-key>"}` to set or rotate the provider
Authorization value, or `DELETE` the same path to revoke it. These routes
require MFA and deploy-write scope. Responses and integration-list metadata
never contain the key; the list shows only `credential_source` and
`credential_configured`. `apid` requires the fleet age **public** recipient,
and `outboundd` receives the matching private identity as a systemd credential.
The ciphertext is fetched and opened for each admitted route request, so a
rotation or deletion takes effect without restarting the gateway. A missing,
corrupt, or revoked key fails closed with 503 before the provider call. See
[ADR-253](../adr/253-customer-sealed-outbound-credentials.md).

An account can create its own integration without an operator pre-provisioning
the origin. Before enabling this workflow, configure
`workload_identity_jwks_path` with vmmd's public signing keys and ensure the
`outboundd` service has the fleet age private identity. `apid` must have the
matching fleet age public recipient. The shipped systemd unit loads the private
identity as a systemd credential; the private key is never placed in the app or
its environment. A newly-created integration will not require an `outboundd`
restart, but changing its JWKS or systemd credential configuration does.

Create the integration with a fixed public HTTPS origin and the maximum routes
any attached app may use. This request-policy example fits the Pro plan:

```json
{
  "name": "payments",
  "origin": "https://api.stripe.com",
  "allowed_methods": ["GET", "POST"],
  "allowed_path_prefixes": ["/v1/customers", "/v1/payment_intents"],
  "daily_request_limit": 10000,
  "request_policy": {
    "rate_per_second": 50,
    "burst": 200,
    "max_in_flight": 100,
    "request_timeout_ms": 2000,
    "max_retries": 1,
    "retry_budget_per_minute": 60,
    "response_cache_ttl_seconds": 60,
    "circuit_breaker_failure_threshold": 5,
    "circuit_breaker_open_seconds": 30
  }
}
```

Send that to `POST /v1/outbound/integrations`, then set the provider header
value with `PUT /v1/outbound/integrations/{id}/credential`, and attach the
integration with `PUT /v1/apps/{slug}/outbound-bindings/{id}`. The creation,
credential, and binding calls require MFA and deploy-write scope. Origin DNS is
checked before it is stored and checked again on every new gateway connection;
private, loopback, and special-use destinations are rejected. The customer's
method/path policy is the integration-wide ceiling; each app binding can narrow
it further. The optional `request_policy` configures rate, burst, concurrency,
upstream timeout, retries, an aggregate retry budget, response-cache freshness,
and the circuit breaker.
If omitted, it defaults to 10 requests/second, burst 20, 10 concurrent
requests, 30 seconds, no retries, no aggregate retry budget, no response
caching, and no circuit breaker. Customer integrations can be changed later with
`PUT /v1/outbound/integrations/{id}/request-policy`; send the full
`request_policy` object shown above. Updates affect subsequent admissions
without an `outboundd` restart. Requests already admitted retain their original
deadline, and reducing `max_in_flight` does not terminate calls already running.
The credential remains unavailable until uploaded and the gateway fails closed
if it is missing or revoked. A maximum of 25 enabled customer integrations is
allowed per account.

The account plan bounds each integration's request policy:

| Plan | Rate/sec | Burst | In flight | Timeout | Max retries | Retry budget/min | Cache TTL |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Free | 10 | 20 | 10 | 30 s | 2 | 60 | 300 s |
| Hobby | 20 | 100 | 50 | 60 s | 2 | 120 | 300 s |
| Pro | 100 | 500 | 250 | 120 s | 2 | 600 | 300 s |
| Scale | 500 | 2,000 | 1,000 | 300 s | 2 | 3,000 | 300 s |

These are configurable ceilings, not included usage. A plan downgrade clamps
the effective policy on subsequent admissions and in API reads; upgrading later
does not automatically raise the customer's saved values. Operator-provisioned
integration policies remain operator-managed through `outboundd.toml`.

`daily_request_limit` is optional and caps admitted calls for this integration
in one UTC day. Its maximum is plan-specific per integration (Free 100,000;
Hobby 1,000,000; Pro 10,000,000; Scale 100,000,000); these are configuration
ceilings, not included request allowances. Omit it to leave the integration
without a customer-selected daily cap. Change or clear it later with
`PUT /v1/outbound/integrations/{id}/budget`, for example
`{"daily_request_limit":5000}`; send `null` to clear it. Read the count and
reset timestamp with `GET /v1/outbound/integrations/{id}/usage`. That response
counts a request when outbound admission grants it, even if the provider later
fails; policy, authentication, rate, concurrency, and daily-limit rejections do
not consume a daily request. A daily-limit rejection is a `429` with
`X-Gregale-Outbound-Rejection: daily_request_limit` and a `Retry-After` to the
next UTC midnight. This is a request-count guard, not a dollar or token budget;
provider pricing and usage units still need provider-specific adapters.
Operator-provisioned integrations can set the same field in their
`outboundd.toml` configuration; the customer API only changes customer-owned
integrations. The rate/burst/concurrency/timeout policy remains independently
configurable and is not inferred from the daily request cap.

An app binding can also have its own daily request cap, independent of the
integration-wide cap. Set or clear it with
`PUT /v1/apps/{slug}/outbound-bindings/{id}/budget`, for example
`{"daily_request_limit":500}`; use `null` to clear it. Read that app's count,
effective limit, UTC usage date, and reset time from
`GET /v1/apps/{slug}/outbound-bindings/{id}/usage`. The binding list also
includes `daily_request_limit`. Each admitted request consumes both the
integration-wide budget, when configured, and the specific app binding's
budget; rejected requests consume neither. The binding limit is bounded by the
account plan, and both counters reset at UTC midnight.

Delete a customer-owned integration with
`DELETE /v1/outbound/integrations/{id}`. This permanently removes its sealed
credential, app bindings, and admission state. This endpoint cannot delete
operator-provisioned integrations. See
[ADR-256](../adr/256-customer-created-outbound-integrations.md) and
[ADR-258](../adr/258-customer-configurable-outbound-request-policy.md).

For every managed integration, set `allowed_methods` (uppercase `GET`, `HEAD`,
`POST`, `PUT`, `PATCH`, or `DELETE`) and `allowed_path_prefixes`. A prefix
matches a whole path segment: `/v1/customers` allows `/v1/customers` and
`/v1/customers/cus_123`, not `/v1/customers-delete`. These permissions apply
to the path following `/i/<integration-id>`; a fixed origin path is prepended
later. Denied requests receive `403 outbound_route_not_allowed` before
admission or provider contact. Managed requests with percent-encoded paths,
dot segments, repeated slashes, backslashes, semicolons, common method-override
headers, or a `_method` query parameter are rejected to avoid obvious bypasses.
An explicit `/` prefix allows
all canonical paths, but should be used only with tightly scoped provider
credentials. This is an HTTP route guard, not a substitute for provider-side
authorization: query parameters and request bodies can still change a
provider operation.

After applying the route-policy migration, existing managed operator rows with
empty permissions fail closed until `outboundd` provisions explicit rules from
its config. New customer-created rows persist explicit rules through `apid`.
Configure operator rules before restarting the daemon. Application-owned
integrations keep their existing behavior and do not accept these managed-only
route fields.

Managed integrations require a vmmd-signed workload identity assertion. Copy
the public JWKS published by the configured vmmd signer to a local file readable
by `outboundd`, then configure `workload_identity_jwks_path` and the exact
`workload_identity_issuer` used by vmmd. The daemon refuses to start a managed
integration without a valid local JWKS. Keep the old and new public keys in
that file during signing-key rotation, restart every `outboundd` replica, then
remove the old key after all assertions signed with it have expired. Drain
older gateway binaries before exposing a managed integration: they may still
accept the legacy shared token.

Customer-created origins are fixed at integration creation and must resolve
only to globally reachable addresses. The gateway's connection-time DNS guard
also prevents later DNS changes from redirecting a connection to a private
destination. A bound app can use the provider
credential through the gateway within its configured HTTP routes, but an
external provider could echo a credential in its own response. Scope provider
keys accordingly. See [ADR-249](../adr/249-platform-held-outbound-provider-authorization.md),
[ADR-251](../adr/251-managed-outbound-route-policy.md), and
[ADR-255](../adr/255-public-destination-dialing.md).

An application calls the gateway explicitly. For a managed integration, fetch
an assertion from the guest-local identity endpoint using the integration's
UUID as the audience suffix:

```sh
ASSERTION=$(curl -fsS "${FAAS_WORKLOAD_IDENTITY_ENDPOINT}?audience=gregale:outbound:${INTEGRATION_ID}" | jq -r .access_token)
curl -H "X-Gregale-Workload-Identity: $ASSERTION" \
     "http://$OUTBOUND_GATEWAY:8095/i/$INTEGRATION_ID/v1/widgets"
```

`outboundd` verifies the signature, issuer, short expiry, and exact
`gregale:outbound:<integration-id>` audience, then takes the app ID from the
signed assertion. It ignores caller-supplied `X-Gregale-App-ID` for managed
integrations and does not forward the assertion to the provider. The assertion
is a short-lived bearer credential, so applications can still copy or misuse
it until it expires; this is not instance-liveness revocation.

For an application-owned provider credential, the legacy request is:

```sh
curl -H "X-Gregale-Outbound-Token: $GREGALE_OUTBOUND_TOKEN" \
     -H "X-Gregale-App-ID: $GREGALE_APP_ID" \
     "http://$OUTBOUND_GATEWAY:8095/i/$INTEGRATION_ID/v1/widgets"
```

The gateway forwards the request to the configured origin, preserving the
path and query. It returns `429 application/problem+json` with `Retry-After`
when the rate or concurrency budget is exhausted; inspect
`X-Gregale-Outbound-Rejection` for `rate_limit` versus `concurrency_limit`.
By default, Gregale makes one upstream attempt. A configured `max_retries`
(0–2) enables retries only for bodyless `GET` and `HEAD` requests, and only
for network timeouts/resets or provider `408`, `425`, `429`, `502`, `503`, and
`504` responses. Other methods and requests with bodies are never retried.
Exponential backoff starts at 100 ms. Automatic retries honor a valid provider
`Retry-After` only when it fits within the one-second retry window; longer
delays are never shortened into an early retry, and the current provider
response is returned instead. Every attempt shares the original
request-timeout deadline. Retries stay inside one admission lease and consume
one daily-budget unit; `outbound_upstream_requests_total` and its latency
histogram record each actual provider attempt. The gateway does not follow
redirects and does not transparently intercept encrypted egress. A final
provider response (including `429`) passes through.
`retry_budget_per_minute` (0–3,000; default 0) optionally adds one token bucket
per integration for extra attempts. A token is consumed only after the retry
backoff, immediately before an eligible second or later upstream attempt; the
first attempt and other integrations do not share that bucket. Capacity is the
configured per-minute value, replenished continuously at that rate, and the
Postgres row lock coordinates all `outboundd` replicas. Customer integrations
are capped by plan (60/120/600/3,000 per minute for Free/Hobby/Pro/Scale); a
plan downgrade clamps the effective bucket on the next admission. If the
budget is empty or its shared state cannot be reached, Gregale stops retrying
and returns the last provider response, or the original transport error,
without replacing it with a platform-generated retry error. A zero budget
leaves the existing per-request `max_retries` behavior unchanged. The
`outbound_retry_budget_events_total` metric records bounded `consumed`,
`exhausted`, `state_error`, and `unavailable` events using the same integration
label policy as the other outbound metrics. See
[ADR-296](../adr/296-shared-outbound-retry-budget.md).
When a provider returns `429` with a valid `Retry-After`, outboundd shares a
per-integration cooldown through Postgres, capped at one hour. Fresh requests
are rejected locally with `429` and
`X-Gregale-Outbound-Rejection: provider_cooldown` until that deadline, so
replicas do not continue sending calls while the provider has asked them to
wait. The original provider `429` remains unchanged; a delay longer than the
one-second retry window is never shortened into an early retry. Eligible cache
hits still return during the cooldown. Updating the integration policy clears
the old provider cooldown. Cooldown checks run after normal admission, so
blocked uncached requests count against configured request budgets. The
`outbound_provider_cooldown_events_total` metric reports bounded `recorded`,
`blocked`, `state_error`, and `unavailable` events. See
[ADR-297](../adr/297-shared-outbound-provider-cooldown.md).
An optional `response_cache_ttl_seconds` (0–300; default 0) enables a
process-local, bounded cache for bodyless `GET` responses with status 200.
Range and conditional requests bypass it. Responses marked `private`,
`no-store`, or `no-cache`, responses with `Set-Cookie`, and `Vary: *` are never
stored; provider `max-age`, `s-maxage`, `Expires`, and `Age` can shorten the
configured TTL. Entries are partitioned by integration, verified app, and an
HMAC of all forwarded request headers, so credentials are not exposed in cache
keys and one app cannot reuse another app's response. The cache is volatile
and local to each outboundd process (1 MiB per entry, 32 MiB and 1,024 entries
per process, with at most eight concurrent cache-fill buffers); restarts and
other replicas start cold. Cache hits still pass
authentication, route checks, rate admission, concurrency limits, and daily
request budgets, but do not make or count an upstream attempt.
If a provider response is interrupted or exceeds the gateway's body cap after
headers have been sent, the gateway aborts the response stream. Callers must
treat the resulting read error as an incomplete response, not a successful
download of the received prefix.

An optional circuit breaker opens after a configured number of consecutive
transient provider failures, then permits one provider probe after its cool-down.
Set both `circuit_breaker_failure_threshold` (1–20) and
`circuit_breaker_open_seconds` (1–300); both default to zero, which disables
the breaker. Network errors, timeouts, and final 408/425/429/5xx responses are
failures; ordinary 4xx responses indicate a reachable provider and reset the
streak. A successful retry also counts as success. Caller cancellation and
local failures before a provider outcome is available do not add a failure; an
inconclusive half-open probe starts another cool-down. The open decision and
exclusive half-open probe are coordinated through Postgres across outboundd
replicas; state resets if the configured breaker policy changes. An open
breaker returns 503 with `X-Gregale-Outbound-Rejection: circuit_breaker_open`
and `Retry-After`. It is checked after normal admission, so an open-circuit
response counts toward configured admission budgets. Eligible response-cache
hits still return while the breaker is open because they do not call the provider. See
[ADR-295](../adr/295-outbound-circuit-breaker.md).

The listener also serves `/metrics` and `/readyz` on port `8095` by default.
Daemon HTTP request metrics record bounded status classes (`1xx` through `5xx`),
request latency, readiness, and standard OTLP exporter health. The outbound
integration metrics described above use only operator-owned integration IDs or
the shared `customer_managed` label; they never include URLs or raw provider
status codes.

## AI gateway integrations

> **Release gate.** The AI gateway is off until an operator enables it.
>
> - Set `ai_gateway_enabled = true` in both `apid.toml` and `outboundd.toml`, or set `FAAS_AI_GATEWAY_ENABLED=1` for both daemons.
> - While it is off:
>   - the AI endpoints return `403 outbound_ai_gateway_unavailable`;
>   - integrations cannot set `ai_provider`;
>   - outboundd refuses AI integrations (`503 outbound_ai_gateway_disabled`) and routes.
>
> See [ADR-991](../adr/991-ai-gateway-hard-caps-providers-visibility.md#release-gate).
>
> **Preview rollout (ADR-998).** With the gate on, the AI gateway serves
> accounts whose plan includes it (Hobby, Pro, Scale).
>
> - During the preview, the `ai_gateway` platform flag limits it to the
>   accounts its rules include ([platform flags](platform-flags.md),
>   ADR-1700). The gate and `ai_gateway_accounts` /
>   `FAAS_AI_GATEWAY_ACCOUNTS` are only its boot fallback until an operator
>   writes the flag.
> - Other accounts get `403 outbound_ai_gateway_unavailable`.
> - A Free account gets `402 plan_feature_gated` from apid and `403` from
>   outboundd.
>
> The rollout steps, alerts and rollback are in the
> [AI gateway runbook](../runbooks/FaasAIGateway.md).

An integration in front of an LLM provider can declare `ai_provider`. Use
`openai` for OpenAI and any provider that speaks its wire format, or
`anthropic` for the Anthropic Messages API. The gateway then reads the response
model and token usage while forwarding each `POST` response, records UTC-day
usage per caller and model, and can stop new calls at a daily token limit. The
provider key, workload identity, route policy and every request policy above
still apply. See [ADR-988](../adr/988-ai-gateway-outbound-integrations.md).

Set it when creating a customer integration, or later:

```json
{
  "name": "llm",
  "origin": "https://api.anthropic.com",
  "allowed_methods": ["POST"],
  "allowed_path_prefixes": ["/v1/messages"],
  "ai_provider": "anthropic",
  "ai_daily_token_limit": 2000000,
  "request_policy": {
    "rate_per_second": 10, "burst": 20, "max_in_flight": 20,
    "request_timeout_ms": 120000
  }
}
```

`PUT /v1/outbound/integrations/{id}/ai` replaces the settings with
`{"ai_provider":"openai","daily_token_limit":5000000}`. Send an empty
`ai_provider` and a null limit to turn AI handling off. The call requires MFA
and deploy-write scope. Operator integrations take `ai_provider` and
`ai_daily_token_limit` in `outboundd.toml`.

`GET /v1/outbound/integrations/{id}/ai-usage?date=YYYY-MM-DD` returns one UTC
day: the provider, the day's token count and limit, and one item per caller
(an app ID, or `runs`) and model. Each item has request, error and
unknown-usage counts plus input, cached input, cache write and output tokens.
Input tokens include cached reads and cache writes for both providers. Rollups
are kept for 90 days; omit `date` for today.

The token limit counts input plus output tokens. Admission rejects a call
(`429`, `X-Gregale-Outbound-Rejection: daily_token_limit`, `Retry-After` to the
next UTC midnight) when either:

- the day's settled total has reached the limit; or
- the settled total, plus the reservations of calls in flight, plus this
  call's own reservation, would pass it.

This makes the limit a hard cap (see "AI hard caps and per-app guardrails"
below).

Usage comes from the provider's own response:

- JSON responses up to 1 MiB are parsed; for larger ones, such as base64
  images, usage is read from the last 64 KiB (ADR-997).
- Streaming OpenAI chat completions include usage only when the request sets
  `"stream_options": {"include_usage": true}`. Without it, the call counts as
  unknown usage.
- A generation or embeddings call with unknown usage settles at its admission
  reservation, not at zero (ADR-996). Token counting and other free calls
  with unknown usage add nothing.
- Non-2xx responses count as errors with no tokens.
- One integration records at most 32 distinct models per day; later ones are
  grouped as `other`.

Every `text/event-stream` and `application/x-ndjson` response, AI or not, is
flushed to the caller as each provider chunk arrives. Its write deadline
follows the request timeout instead of the daemon's `write_timeout`, so set
`request_timeout_ms` high enough for your longest generation.

AI metrics keep the bounded integration label and never include model names:
`outbound_ai_requests_total` (by `known`, `unknown` or `error` outcome),
`outbound_ai_tokens_total` (by `input`, `cached_input`, `cache_write` and
`output` kind), `outbound_ai_time_to_first_byte_seconds` and
`outbound_ai_usage_record_events_total`. The dependency span also carries
OpenTelemetry GenAI attributes (`gen_ai.system`, `gen_ai.response.model` and
`gen_ai.usage.*`).

A call's usage settles once, keyed by its admission lease: outboundd makes up
to three 5-second attempts, and a retry after a commit that landed changes
nothing. `outbound_ai_usage_record_events_total` counts `recorded`, `retried`,
`state_error` (every attempt failed) and `unavailable` (ADR-1120).

Apps reach these integrations through the app relay below, so stock SDKs work
with only a base URL:

```sh
OPENAI_BASE_URL="$GREGALE_OUTBOUND_URL/i/$INTEGRATION_ID/v1"   OPENAI_API_KEY=unused
ANTHROPIC_BASE_URL="$GREGALE_OUTBOUND_URL/i/$INTEGRATION_ID"   ANTHROPIC_API_KEY=unused
```

The SDK's placeholder key is replaced by the integration's credential.
Anthropic integrations receive it in `x-api-key`, with any `Bearer ` prefix
removed.

### Suspended accounts

Calls through a customer integration of an account that is `suspended` or
`deleted_pending` are refused with `403 outbound_account_suspended`, from
the app relay and from access tokens alike (ADR 20261027132936).
`past_due` accounts keep calling. Reading a logged call's captured bodies
(`GET .../ai-requests/{request}/content`) needs `admin` or `secrets:read`.

## App relay

Every app VM has `GREGALE_OUTBOUND_URL=http://127.0.0.1:2774`. Requests to
`$GREGALE_OUTBOUND_URL/i/<integration-id>/<provider path>` reach `outboundd` as
the calling app, with no network route, gateway token or identity header.
Streaming works in both directions. See
[ADR-989](../adr/989-app-outbound-relay.md).

How a request travels:

1. guest-init forwards each loopback connection unchanged over the VM's vsock
   port 1032.
2. vmmd identifies the instance from that per-VM listener, mints a short-lived
   relay assertion, and splices the bytes to `outboundd`'s relay listener.
3. `outboundd` checks the assertion once per connection and then handles the
   requests as that app.

Only managed integrations are reachable this way. Token-authenticated
integrations return `403 outbound_relay_not_supported`. Bindings, route
policy, budgets, caching and AI usage all apply as usual.

Enable the relay in two places:

- **outboundd.toml.** Set `relay_listen`:
  - Single box: `unix:///run/faas/outboundd-relay.sock`.
  - Split box: `tcp://0.0.0.0:8096`, with `relay_tls_cert_path`,
    `relay_tls_key_path` and `relay_tls_ca_path` pointing at the
    `outboundd/server` leaf and the fleet CA.

  The relay needs `workload_identity_jwks_path`. On a split box, clients must
  present a fleet certificate whose name is a registered compute node.
- **vmmd.** Set `faas_vmmd_outbound_relay_target`:
  - The manifest renders `tcp://outboundd.faas:8096` for compute hosts.
  - Single-box hosts set the Unix socket path.

  The TCP target uses the `vmmd/outboundd-client` node-identity leaf. Issue
  the two new leaves (`outboundd/server`, `vmmd/outboundd-client`) with the
  usual PKI run. Until they exist, vmmd logs `outbound relay disabled` and
  keeps the relay closed. VM hosting is unaffected.

The control-plane firewall opens TCP 8096 to compute CIDRs. When the relay is
off, apps get an immediate connection error from `GREGALE_OUTBOUND_URL`.
`outbound_relay_connections_total` counts accepted, rejected and overload
connections. Qualification candidates never get the relay.

## AI gateway budgets, prices and cost

See [ADR-990](../adr/990-ai-gateway-budgets-fallback-cost.md). All settings
below require MFA and deploy-write scope. The `gregale ai` command wraps
them.

**Per-app token limit.**
`PUT /v1/apps/{slug}/outbound-bindings/{id}/ai-budget` with
`{"daily_token_limit": 500000}` sets one app's own UTC-day input plus output
token limit. Other apps on the integration are unaffected; send `null` to
clear it.

**Price list.** `PUT /v1/outbound/integrations/{id}/ai-prices` replaces the
list:

```json
{"entries": [
  {"model_prefix": "gpt-4o-mini", "input_millicents_per_mtok": 15000,
   "cached_input_millicents_per_mtok": 7500, "output_millicents_per_mtok": 60000},
  {"model_prefix": "*", "input_millicents_per_mtok": 250000, "output_millicents_per_mtok": 1000000}
]}
```

- Prices are US-dollar millicents per million tokens; $0.15 is 15,000. Cost
  limits and costs are in US dollars too (ADR 20261019123822).
- The longest matching model prefix wins, and `*` matches any model.
- An absent cached-input or cache-write price uses the input price.
- A new list applies to calls settled from then on. Earlier lists stay, so
  recorded costs never change.
- Each call's cost is rounded up to a whole millicent.
- A model the list does not name is priced from the built-in catalog (see
  below). Add a `*` entry, even at zero, to price only the models you list.

**Cost limits.** `PUT /v1/outbound/integrations/{id}/ai` also takes
`daily_cost_limit_millicents` and `monthly_cost_limit_millicents` (UTC day
and calendar month). Once settled cost reaches a limit, new calls get `429`
with `X-Gregale-Outbound-Rejection: daily_cost_limit` or
`monthly_cost_limit`. Like token limits, cost limits count each in-flight
call's priced reservation, so they are hard caps.

**Usage report.** `ai-usage` reports each item's `cost_millicents`, the day's
and month-to-date settled cost, and both limits.

## AI retries, cache and OpenAI stream usage

For AI integrations, a `POST` body of up to 4 MiB is kept in memory for the
call:

- **Retries:** `max_retries` and the retry budget also apply to these calls,
  on the usual transient failures. Every retry happens before any response
  byte reaches the caller.
- **Cache:** with `response_cache_ttl_seconds` set, an identical JSON
  request (same body, caller and headers) is served from the cache when the
  provider returned `200`. The key leaves out per-call telemetry headers
  (`traceparent`, `tracestate`, `baggage`, `sentry-trace`, `b3`, `x-b3-*`,
  `x-request-id`, `x-correlation-id`, `x-amzn-trace-id`,
  `x-cloud-trace-context`, `user-agent`, `idempotency-key`, `x-stainless-*`;
  ADR 20261027132936), and a hit skips the moderation check. Streams are cached too (ADR-1440): the stream is
  copied into the cache as it is forwarded and kept only when it ends cleanly
  within 1 MiB, and a hit replays it at once. Each outboundd process holds at
  most 32 MiB of entries.
- **Cache hits** cost no tokens. Each settles as one of the caller's and
  model's `cached_requests` and adds the cost the provider call would have had,
  priced from the cached response, to `cache_saved_millicents`.
  `outbound_ai_response_cache_saved_millicents_total` counts it.
- **Prompt-cache savings:** every call also adds
  `prompt_cache_saved_millicents`: its cost with cached reads and cache
  writes priced as plain input, minus its cost. It is negative when cache
  writes outweighed reads. `gregale ai usage` shows both savings.
- **OpenAI stream usage:** the gateway turns on
  `stream_options.include_usage` for OpenAI streaming completions. It records
  the extra usage chunk, then removes it from the stream the caller sees, so
  these calls no longer count as unknown usage.

Larger bodies stream exactly as before, without these features.

### AI call bounds

See [ADR 20261027132936](../adr/20261027132936-ai-gateway-release-review-fixes.md).
For a billable AI call:

- The integration's `request_timeout_ms` bounds the wait for the response
  headers, retries and route fallback included. An AI integration created
  without a request policy starts at its plan's ceilings
  (`gregale ai request-policy <id>` shows and sets them).
- After the headers, the call runs until `OutboundAICallMaxSeconds` (30
  minutes) after it started, and a streamed answer ends after
  `OutboundAIStreamIdleSeconds` (10 minutes) without a byte. The response's
  write deadline moves to the call's end, so outboundd's `write_timeout`
  does not cut it.
- The admission lease is renewed every third of max(timeout, 15 s) while
  the call runs, so its reservation and in-flight slot count until
  settlement. A crashed outboundd's leases expire within that span.
- A call whose request was sent but that ended before its headers settles
  at its reservation as unknown usage, logged with status `504` (the
  timeout) or `499` (the caller left).

## AI automatic prompt caching


`gregale ai policy <id> --prompt-cache on` (or `auto_prompt_cache` in the AI
policy) lets the gateway add prompt-cache breakpoints to Claude requests that
carry none (ADR-1437). It covers:

- the Anthropic Messages API;
- Vertex AI's Anthropic endpoints;
- Bedrock InvokeModel and Converse for Claude models;
- OpenAI-format routes to any of these.

A request that sets its own `cache_control`, or `cachePoint` on Converse, is
left as it is.

The gateway adds at most two breakpoints, both with the default five-minute
lifetime:

- **Prefix:** at the end of the system prompt, or of the tool list when there
  is no system prompt. It is added when the prefix is at least about 1,024
  tokens (4 KiB). The same tools and system prompt for the same model must
  have been seen on the integration in the last five minutes, or the call
  must name `X-Gregale-AI-Session`. A prompt sent once is never marked.
- **Conversation:** at the last eligible block of the last message, when the
  request carries earlier turns or names `X-Gregale-AI-Session`, and the
  whole prompt is at least about 1,024 tokens. The next turn reads the
  conversation from the cache. Thinking blocks are never marked.

The response names what was added in `X-Gregale-AI-Prompt-Cache:
prefix,conversation`, and `outbound_ai_prompt_cache_marks_total` counts the
marks.

**Costs:**

- A cache write costs 1.25 times the input price and a read 0.1 times or less.
  Usage reports cached and cache-write tokens and prices them from the price
  list.
- A conversation that stops after one more turn, or a prefix seen twice and
  never again, pays the write premium once without a read.
- Each outboundd process tracks repeated prefixes on its own, so with several
  replicas a prefix may need one more call per replica before it is marked.

OpenAI and Gemini cache prompts without markers, so the setting changes
nothing for them. A marked body is re-encoded as JSON: key order changes, the
content does not.

Prefixes are shared across outboundd replicas (ADR 20261019132139). A replica
that has not seen a prefix asks `outbound_ai_prompt_prefixes`, which holds
SHA-256 hashes only, and marks it if any replica saw it within five minutes.
The lookup is bounded at 200 ms and fails open to the replica's own memory.
Rows are pruned hourly after ten minutes, and
`outbound_ai_prompt_cache_shared_total{result}` counts lookups (repeated,
first, refreshed, error).

## AI fallback routes

A route is an ordered list of one to five managed AI integrations of one
`ai_provider`. Each target can optionally override `model`.

- Create one with `POST /v1/outbound/ai-routes` or
  `gregale ai routes create --name chat --target <primary-id> --target <backup-id>:gpt-4o-mini`.
- Call it through the relay at `$GREGALE_OUTBOUND_URL/r/<route-id>/<provider path>`.
- The gateway tries each target through its normal integration path, so the
  app must be bound to every target.
- It moves to the next target after a budget or availability rejection
  (`429`, or `503` from an open circuit or provider cooldown), a provider
  `408`, `429` or `5xx`, or a transport error, always before any response
  byte is sent.
- A `400`, `403` or `404` returns as is.
- `X-Gregale-AI-Route-Target` names the integration that answered, and
  `outbound_ai_route_fallbacks_total` counts the hops.

## AI hard caps and per-app guardrails

See [ADR-991](../adr/991-ai-gateway-hard-caps-providers-visibility.md).

**Reservations.** Before admission, every AI `POST` reserves tokens and cost:

- **Input:** one token per 4 body bytes.
- **Output:** the request's own output ceiling (`max_tokens`,
  `max_completion_tokens`, `max_output_tokens`,
  `generationConfig.maxOutputTokens` or `inferenceConfig.maxTokens`). When
  there is none, the app's ceiling, else 4096.
- **Cost:** the reservation priced from the current price list.

The reservation lives on the call's admission lease and counts against every
token and cost limit until the call settles. A request asking for more output
than the remaining budget is refused instead of overshooting. Lower its
output ceiling, or wait for the reset.

**Per-app guardrails.**
`PUT /v1/apps/{slug}/outbound-bindings/{id}/ai-guardrails` (or
`gregale ai guardrails`) sets, for one app on one AI integration:

- **`requests_per_minute`:** admitted calls per UTC minute. Over the limit:
  `429 requests_per_minute_limit`.
- **`tokens_per_minute`:** settled tokens plus live reservations per UTC
  minute. Over the limit: `429 tokens_per_minute_limit`.
- **`allowed_models`:** exact names or prefixes ending in `*`. Any other model
  gets `403 outbound_ai_model_not_allowed`. For Gemini, Vertex, Bedrock and
  Azure deployment paths the model comes from the path.
- **`max_output_tokens`:** a higher requested ceiling gets
  `400 outbound_ai_max_tokens_exceeded`. A request with no ceiling gets one
  added in its format's field.

A guarded app's AI bodies must be at most 4 MiB, so the gateway can read them.
A Bedrock `invoke` body for a non-Anthropic model cannot take a ceiling; use
Converse instead.

## More AI providers

| `ai_provider` | Origin | Stored credential |
| --- | --- | --- |
| `gemini` | `https://generativelanguage.googleapis.com` | the API key, sent in `x-goog-api-key` |
| `azure_openai` | `https://<resource>.openai.azure.com` | the API key, sent in `api-key`; or `Bearer <Entra token>` |
| `bedrock` | `https://bedrock-runtime.<region>.amazonaws.com` | `aws:<access-key-id>:<secret-access-key>[:<session-token>]`; every call is SigV4-signed |
| `vertex` | `https://<region>-aiplatform.googleapis.com` | the service-account key JSON on one line (`jq -c . key.json`), or `Bearer <access token>` |

Notes per provider:

- **Bedrock:** request bodies must be at most 4 MiB, because signing hashes
  the body. Usage is read from Converse `usage`, from the
  `X-Amzn-Bedrock-*-Token-Count` headers, and from the binary event stream of
  `converse-stream` and `invoke-with-response-stream`.
- **Vertex:**
  - outboundd exchanges the service-account key at
    `https://oauth2.googleapis.com/token` and caches the access token. A key
    naming any other `token_uri` is refused.
  - The usage format follows the path: `publishers/google` is Gemini,
    `publishers/anthropic` is Anthropic, and `endpoints/openapi` is OpenAI.
- **Gemini:** thinking tokens count as output.

## OpenAI-format routes

`gregale ai routes create --name any-chat --format openai --target <openai-id> --target <anthropic-id>:claude-sonnet-4 --target <gemini-id>:gemini-2.5-flash`
creates a route that accepts OpenAI chat completions and embeddings. Each
target can be:

- **`openai`:** receives the request as is.
- **`azure_openai`:** receives it at `/openai/v1/chat/completions`, with the
  target model as the deployment.
- **`anthropic`:** translated to the Messages API, with the response
  translated back.
- **`gemini`:** translated to `generateContent`, with the response translated
  back.
- **`bedrock`:** translated to Converse (`/model/{id}/converse`, or
  `converse-stream`, whose binary event stream is translated into SSE). The
  target model is a Bedrock model ID, inference profile or ARN.
- **`vertex`:** Claude models go to `rawPredict` with the Anthropic
  translation, other models to Gemini `generateContent`. Prefix the model
  with `anthropic/` or `google/` to choose the publisher explicitly. The
  integration's allowed path prefixes must name exactly one
  `/v1/projects/{project}/locations/{location}`; route creation refuses a
  Vertex target without one.

The translation covers:

- system and developer messages;
- text and image parts (Gemini takes `data:` images only);
- tools, tool calls, tool results and `tool_choice`;
- `stop`, `temperature`, `top_p`, the output ceiling and streaming;
- `response_format` (see below).

**Structured output.** `json_schema` becomes Anthropic's
`output_config.format`, Gemini's `responseJsonSchema` and Bedrock's
`outputConfig.textFormat`. `json_object` becomes Gemini JSON mode. Anthropic
and Bedrock always need a schema, so `json_object` is refused for them with
`400 outbound_ai_translation_unsupported`; its `detail` says why. The schema
is passed through unchanged, so each provider's JSON Schema subset applies.

**Embeddings.** `POST $GREGALE_OUTBOUND_URL/r/<route-id>/v1/embeddings` takes a
string or an array of strings:

- OpenAI receives it as is, and Azure OpenAI at `/openai/v1/embeddings`.
- Gemini receives `batchEmbedContents`, and Vertex AI Google models
  `predict`. `dimensions` becomes the output dimensionality.
- Anthropic and Bedrock targets are refused.

`encoding_format: base64`, the OpenAI SDK default, is supported. Use one
embeddings model per route: vectors from a fallback target with another model
are not comparable. `X-Gregale-AI-Route-Target` names the target that served.
Vertex embeddings report token counts and count toward budgets. Gemini
embeddings report none, so they settle as usage unknown at their reservation.

Callers see OpenAI JSON and `chat.completion.chunk` events, with a usage chunk
only when they ask for one. `n` above 1 gets
`400 outbound_ai_translation_unsupported`. That problem's `detail` says what
could not be translated.

Point any OpenAI SDK at `$GREGALE_OUTBOUND_URL/r/<route-id>/v1`. Usage and cost
are still read from each provider's native response.
`outbound_ai_translations_total` counts translated attempts.

**Responses API.** openai-format routes also accept `POST …/v1/responses`
(ADR-996):

- OpenAI receives it as is, and Azure OpenAI at `/openai/v1/responses`.
- Other targets get it as a chat completion. The reply comes back as a
  Responses object, or as Responses stream events from `response.created` to
  `response.completed`.
- Translated targets are stateless: send the whole conversation in `input`.
  `previous_response_id`, `conversation`, stored prompts, `background`,
  OpenAI-hosted tools and file inputs get
  `400 outbound_ai_translation_unsupported`.

**Forced tool choice.** Claude Fable 5.1, Mythos 5.1, Opus 5.5 and Sonnet 5.5
reject a forced `tool_choice`. For these models a route sends `auto` plus a
system instruction to call the tool, so the model usually calls it but may
answer in text. Check `finish_reason` before reading `tool_calls`.

## Built-in price catalog

See [ADR-994](../adr/994-ai-gateway-price-catalog-end-users-affinity.md).
outboundd and apid embed `pkg/outbound/aiprices/catalog.json`: list prices for
about 850 models across the six providers, in US-dollar millicents per million
tokens: the providers' list prices with no exchange rate (ADR 20261019123822). A call's model is priced from the integration's price list first, then
from the catalog for the integration's provider. Reservations and settlement
use the same price.

- The file records its LiteLLM source commit, generation date and currency.
  Prices are base-tier list prices; batch, priority, long-context tiers
  and negotiated discounts are not modelled.
- A dated or versioned model the catalog lacks is priced as its family
  (`gpt-4o-2024-08-06` as `gpt-4o`, `claude-sonnet-4-5@20250929` as
  `claude-sonnet-4-5`). Bedrock cross-region prefixes and ARNs are stripped.
- `gregale ai catalog <provider> [--model NAME]` (or
  `GET /v1/outbound/ai-price-catalog?provider=&model=`) shows the prices.
- To refresh the catalog, download LiteLLM's
  `model_prices_and_context_window.json` at a pinned commit into its own
  directory, run `python3 -I scripts/gen_ai_price_catalog.py` with that
  commit, and review the diff in a PR.
- A model in neither the price list nor the catalog has no price. While a
  cost cap applies to the call (the integration's daily or monthly cost
  limit, the end-user cost limit for a named end user, a tag cost limit on
  one of its tags, a trace cost limit), it is refused with
  `403 outbound_ai_model_unpriced` unless `allow_unmetered` is on; an
  unmetered realtime response ends the session. An answer whose own model has
  no price is priced as the requested model. Settled calls that cost zero for
  want of a price count in `outbound_ai_unpriced_calls_total`
  (ADR 20261027132936).

## Per-end-user limits

Apps that serve their own users can name each call's end user with
`X-Gregale-AI-User`:

- The value is 1 to 64 letters, digits or `. _ : @ + = / | -`.
- An invalid or repeated header gets `400 outbound_ai_user_invalid`.
- The gateway never forwards it to the provider.
- Send an opaque ID or a hash, not an e-mail address. The value is stored in
  the per-user rollup for 31 days and, with the request log on, in the log for
  7 days.

`gregale ai policy <id> --user-daily-tokens 200000 --user-daily-cost 2` sets
`user_daily_token_limit` and `user_daily_cost_limit_millicents`. They cap
every named end user's UTC day as hard caps: settled usage, plus that user's
in-flight reservations, plus the call's own reservation. A call over a cap
gets `429` with `X-Gregale-Outbound-Rejection: user_daily_token_limit` or
`user_daily_cost_limit`. Calls without the header are limited only by the
integration and app budgets.

`gregale ai users <id> [--date D] [--user ID]` (or
`GET .../ai-usage/users`) lists a day's end users by cost. Each integration
tracks up to 10,000 distinct end users per UTC day. With per-user limits set,
a new end user after that gets `429` with
`X-Gregale-Outbound-Rejection: user_tracking_full` until the next UTC day;
users already tracked that day are unaffected (ADR-996). Without per-user
limits, later users are counted under `~other`.

**Alerts.** With per-user limits set, an end user's day alerts at 80% and 100%
of each limit, once per user, day and threshold:

- the budget is `user_daily_tokens` or `user_daily_cost`;
- the `outbound.ai_budget.threshold` webhook carries `end_user` and goes only
  to the app whose call crossed the threshold;
- `gregale ai alerts` shows the end user;
- these alerts are kept for 31 days.

**Export.** `gregale ai users export <id> --from 2026-10-01 --to 2026-10-31 >
ai-users.csv` (or `GET .../ai-usage/users/export?from=&to=&format=csv`)
streams every end user's row for each day in the range, ordered by day and end
user. The columns are `usage_date, end_user, requests, error_requests,
input_tokens, output_tokens, tokens, cost_millicents`. The API's default
format is NDJSON.

**App helpers.** The Node and Python SDKs build the relay URL and headers:

```ts
import OpenAI from 'openai';
import { aiGatewayURL, aiGatewayHeaders, opaqueAIUserId } from '@gregale/sdk-node/preview';

const client = new OpenAI({ baseURL: `${aiGatewayURL({ route: routeId })}/v1`, apiKey: 'unused' });
const user = opaqueAIUserId(email, process.env.USER_ID_SECRET);
await client.chat.completions.create(body, { headers: aiGatewayHeaders({ user, session: chatId }) });
```

Python offers `ai_gateway_url`, `ai_gateway_headers` and `opaque_ai_user_id`
from `faas_sdk`. `opaqueAIUserId` turns an e-mail address into a stable
`u_…` ID. Pass a secret so the ID cannot be traced back to the address.

## AI usage tags

An AI call can name up to five tags with `X-Gregale-AI-Tags:
feature=search,tier=free` (ADR-1436):

- A key is 1 to 32 lowercase letters, digits, `_`, `.` or `-` that starts with
  a letter. A value is 1 to 64 letters, digits or `. _ : @ + / -`.
- Keys must be distinct, and the whole list goes in one header. A malformed
  header gets `400 outbound_ai_tags_invalid`. The gateway never forwards it.
- Like end-user IDs, tag values are stored for 31 days in the rollup and, with
  the request log on, in the log. Do not put personal data in them.

**Usage.** `gregale ai tags <id> [--date D] [--key K]` (or
`GET .../ai-usage/tags`) lists a day's tags by cost, with each tag's limit.

- Each integration tracks up to 1,000 distinct tags per UTC day. Later new
  tags add to their key's overflow row, `<key>=~other`, so each key keeps a
  total. A tag with a limit always keeps its own row.
- Request-log entries carry `tags`. The log-export line carries a `tags`
  object, and OTLP drains get `gregale.ai.tags`.

**Limits.** `gregale ai tag-limits <id> --set feature=search:20` (or `PUT
.../ai-tag-limits`) caps one exact tag's UTC-day cost. An integration holds up
to 20 limits; `--remove` drops one and `--clear` drops them all.

- A call carrying a limited tag is refused when the tag's settled cost today,
  plus the reservations of the tag's in-flight calls, plus the call's own
  reservation, would pass the limit. It gets `429` with
  `X-Gregale-Outbound-Rejection: tag_daily_cost_limit` until the next UTC day.
- Calls without the tag are unaffected. To cap each customer instead of one
  tag, use the per-end-user limits above.

**Alerts.** A tag with a limit alerts at 80% and 100% of it, once per tag, UTC
day and threshold (ADR-1438):

- the budget is `tag_daily_cost`, and the alert names the `tag`;
- the `outbound.ai_budget.threshold` webhook carries `tag` and goes only to
  the app whose call crossed the threshold;
- `gregale ai alerts` shows the tag;
- these alerts are kept for 31 days. Overflow rows never alert.

**Export.** `gregale ai tags export <id> --from 2026-10-01 --to 2026-10-31
--key customer > ai-tags.csv` (or `GET .../ai-usage/tags/export?from=&to=&key=&format=csv`)
streams every tag row of each day in the range, ordered by day and tag. The
columns are `usage_date, tag, requests, error_requests, input_tokens,
output_tokens, tokens, cost_millicents`. The API's default format is NDJSON.

The tag check joins admission's existing database read, so it adds no round
trip.

## AI traces

An AI trace groups the calls of one task, such as one agent run that calls the
model, a tool and the model again (ADR 20261020202035). A call belongs to the
trace named by:

- `X-Gregale-AI-Trace`: an ID of 1 to 64 letters, digits or
  `. _ : @ + = / | -`. A malformed header gets `400 outbound_ai_trace_invalid`.
  The gateway never forwards it.
- otherwise the trace ID of a valid W3C `traceparent` header, so apps that
  already trace their requests get AI traces with no change. `traceparent` is
  still forwarded.

A call with neither belongs to no trace.

**Recording.** Traces are recorded when the integration keeps a request log or
has trace limits.

- Each trace keeps its first and last call, calls, errors, calls with unknown
  usage, tokens, cost, the tool calls the model requested, and up to 8 models.
  The first call's app and end user name the trace.
- The gateway reads the tool calls a model requests from OpenAI Chat
  Completions and Responses, Anthropic Messages (also through Bedrock),
  Gemini, Vertex and Bedrock Converse responses, streamed or not, up to 16 per
  response. It keeps the tool names and a hash of the arguments, never the
  arguments themselves.
- Request-log entries carry `trace_id` and `tool_calls`. The log-export line
  carries `trace_id`, and OTLP drains get `gregale.ai.trace_id`.
- Each integration records up to 100,000 new traces per UTC day. Calls of
  later new traces still settle and are logged, but no trace is recorded.
- Traces are kept for 7 days, like the request log.
- A realtime session belongs to the trace of its upgrade request. Each
  response or Live turn adds to the trace, but its tool calls are not read.

**Viewing.** `gregale ai traces <id> [--app APP-ID] [--user USER] [--limit N]`
(or `GET .../ai-traces?app_id=&end_user=&before=&limit=`) lists traces, newest
last call first, up to 200 per page. `gregale ai traces <id> <trace-id>` (or
`GET .../ai-traces/{trace}`) shows one trace and up to 500 of its logged
calls in order, with each call's model, status, tokens, cost, duration, time
to first byte and requested tools. Without a request log the trace has totals
but no calls.

**Limits.** `gregale ai policy <id>` takes:

| Flag | Policy field | Refuses a trace's next call when |
| --- | --- | --- |
| `--trace-max-calls N` | `trace_max_calls` | the trace's settled and in-flight calls reach N (1 to 1,000,000) |
| `--trace-max-cost USD` | `trace_max_cost_millicents` | settled cost, plus in-flight reservations, plus the call's own reservation would pass the limit |
| `--trace-max-repeated-tool-calls N` | `trace_max_repeated_tool_calls` | N consecutive calls requested the same tools with the same arguments (1 to 1,000) |

`none` clears a limit. A refused call gets `429 outbound_budget_exhausted`
with `X-Gregale-Outbound-Rejection` set to `trace_call_limit`,
`trace_cost_limit` or `trace_tool_loop`, and no `Retry-After`: a trace never
resets, so start a new one.

- A response with no tool calls ends a repeat streak; an error leaves it as it
  was.
- Calls of one trace can run in parallel, so "consecutive" is the order they
  settle in, and the calls already in flight when a limit is reached can pass
  it by up to their reservations.
- With trace limits set, a call of a new trace past the day's 100,000 is
  refused with `trace_untracked`, since it could not be limited.

## AI calls by deployment

Settlement adds an app instance's call to its deployment's row for the
current UTC minute in `outbound_ai_deployment_minutes` (ADR 20261022153155).
The deployment is read from `instances` in the same statement, by the
verified workload identity's instance and app; a call whose instance row is
gone is not attributed. Rows have no foreign keys and are pruned after
`OutboundAIDeploymentStatsRetentionDays` (14) with the other AI rollups.

| Reader | Reads |
| --- | --- |
| `ai` release gates | The release's and the baseline's rows in the gate's windows, through the primary key. |
| `GET /v1/apps/{slug}/ai-deployments` | The app's rows of the last 14 days through `(app_id, minute)`, summed by deployment, at most 50. |

An `ai` gate is skipped by meterd and apid N-1, and an N-1 apid refuses a
rollout decision carrying an `ai` result. Roll apid back only after the
`ai` gates are removed, with `gregale release-gates rm --app <slug> <name>`
for each one; the down migration removes any that are left.

## AI gateway dashboard

The dashboard's AI pages read what the API serves (ADR 20261022145950). The
same gate applies: the platform flag, the account's rollout and the plan;
a gated account sees why.

| Page | Shows |
| --- | --- |
| `/dashboard/ai` | Each AI integration with today's calls, error rate, cost, quality flags and mean score. |
| `/dashboard/ai/{integration}` | One UTC day (last 90): cost and quality by model and by caller, the 20 most expensive tags and end users (last 31 days), latency and errors by model, the latest drift alerts and traces. |
| `/dashboard/ai/{integration}/traces` | Traces newest first, filtered by the first call's app or end user, 50 per page. |
| `/dashboard/ai/{integration}/traces/{trace}` | One trace's logged calls as a timeline, with each call's model, tokens, cost, time to first byte, requested tools, quality flags, rating and score. |

Latency and error rate by model come from the latest 5,000 logged calls of
the last 24 hours, read through the request log's (integration, time)
index: the p50 and p95 time to first byte and duration. Without the request
log the page says so.

## AI quality signals

The gateway reports the quality of AI answers it can see without reading them
for meaning (ADR 20261021171712).

**Call IDs.** Every AI call's response carries `X-Gregale-AI-Call: <uuid>`,
the ID of its request-log entry. The log-export line carries it as `call_id`
and OTLP drains get `gregale.ai.call_id`. Realtime turns get IDs in the log,
but a WebSocket has no per-turn header.

**Quality flags.** A call that answered (2xx with known usage) of a text
generation endpoint is marked with:

| Flag | When |
| --- | --- |
| `truncated` | The model stopped at the output-token limit (`finish_reason` `length`, `stop_reason` `max_tokens`, `MAX_TOKENS`, `max_output_tokens`). |
| `refused` | The provider refused or filtered the answer: a refusal field, `content_filter`, Anthropic `refusal`, a Gemini safety block, a Bedrock guardrail. |
| `empty` | The answer had no text and requested no tools. Thoughts do not count as text. |
| `invalid_json` | JSON was asked for (`response_format` or `text.format` `json_object`/`json_schema`, Gemini `responseMimeType: application/json`) and the answer is not JSON; a tool call's arguments are not JSON; or Gemini reports `MALFORMED_FUNCTION_CALL`. Up to 64 KiB of text and 16 KiB of arguments per call are checked. |
| `tool_error` | The newest message of the request reports a failed tool: an Anthropic `tool_result` with `is_error: true` or a Bedrock `toolResult` with `status: error`. |

Flags are on request-log entries (`quality_flags`), the log-export line,
`gregale.ai.quality_flags`, the daily usage (`truncated_requests` and the
rest, the QUALITY column of `gregale ai usage`) and
`outbound_ai_quality_flags_total{integration_id, flag}`. They describe the
provider's answer, before response guardrails change it.

**Feedback.** An app rates its own logged call with `POST
/i/<integration>/_gregale/feedback` and `{"call_id": "...", "rating":
"good"|"bad", "label": "wrong_answer"}`, authenticated as for AI calls. For a
route call, use the integration `X-Gregale-AI-Route-Target` names.

- Feedback needs the request log; a call that is not logged, or that another
  app made, gets `404 outbound_ai_call_not_found`.
- A later rating replaces the earlier one. The label is optional: up to 32
  lowercase letters, digits, `_`, `.` or `-`, starting with a letter. There
  is no free text.
- A person can rate any logged call with `gregale ai feedback <id> <call-id>
  --good|--bad [--label L]` (or `PUT .../ai-requests/{request}/feedback`).
- Ratings leave with the request log after 7 days; the daily usage keeps
  `good_feedback` and `bad_feedback`.

**Online evaluations.** `gregale ai policy <id> --online-eval-percent 5
--online-eval-judge <integration-id> --online-eval-model <model>
[--online-eval-rubric TEXT]` (policy field `online_evaluation`) scores that
share of answered app calls from 1 to 5.

- It needs `--request-log-content on`: only calls whose bodies were captured
  whole are sampled. The judge integration must be one of the account's
  enabled AI integrations.
- outboundd's worker sends the redacted request and answer text, and the
  rubric, to the judge. The call is metered and capped under the caller
  `evaluation` on the judge integration.
- At most 1,000 calls per integration per UTC day are evaluated. A call whose
  capture expired, or whose judge reply has no score, counts as a judge
  error.
- Scores are on request-log entries (`evaluation_score`) and in the daily
  usage (`evaluated_requests`, `evaluation_score_sum`, `evaluation_errors`;
  the SCORE column).

**Drift alerts.** With `--quality-alerts on`, outboundd compares each model's
UTC day so far with its previous 7 days every 10 minutes, over app and Runs
calls:

- A flag rate, or the share of bad ratings, alerts when it is at least 5
  percentage points and at least twice the baseline. The day needs 100
  answered calls and the baseline 500; for ratings, each side needs 20 rated
  calls.
- The mean evaluation score alerts when it drops by at least 0.5, with 20
  evaluations on each side.
- Each alert fires once per integration, model, metric and UTC day, goes to
  every bound app as `outbound.ai_quality.drift`, and is listed by `gregale
  ai quality-alerts <id>` (or `GET .../ai-quality-alerts`) for 31 days.
  Rates are basis points and scores hundredths.

## Provider key pools

`gregale ai keys add <id> --label second --from-stdin` adds an extra sealed
key. Pipe the value in; the CLI never accepts it as a flag. An integration may
hold up to 8 extra keys in addition to its primary credential.

- outboundd picks keys round-robin. A call with `X-Gregale-AI-Session`, or
  else `X-Gregale-AI-User`, picks its key by rendezvous hash instead, so one
  conversation keeps using the same key and that provider account's prompt
  cache. Removing a key moves only the sessions that were on it.
- A key that returns `429` cools down for its `Retry-After`, or 30 seconds,
  and the call moves at once to the next key. This costs neither a retry nor
  the retry budget.
- The integration-wide provider cooldown applies only when every key is
  limited.
- Keys are listed by ID and label only. `gregale ai keys rm` removes one, and
  outboundd stops using it on its next call.
- `outbound_credential_key_events_total` counts cooldowns, rotations and
  exhausted pools.

## AI budget alerts and request log

**Budget alerts.** When settlement first reaches 80% or 100% of a budget in
its period, outboundd records an alert. The budgets are:

- the integration's daily tokens;
- an app's daily tokens;
- the daily cost;
- the monthly cost.

It also enqueues an `outbound.ai_budget.threshold` app webhook:

- an integration budget notifies every bound app's subscribed webhooks;
- an app budget notifies only that app's.

`gregale ai alerts <id>` (or `GET .../ai-alerts`) lists alerts, which are kept
for 90 days. `outbound_ai_budget_alerts_total` counts them.

**Request log.** `gregale ai policy <id> --request-log on` makes every settled
AI call write one metadata row:

- caller, end user, route, model and status;
- usage outcome, tokens and cost;
- duration, time to first byte and attempts.

Headers are never stored, and cache hits are not logged. `gregale ai logs
<id>` (or `GET .../ai-requests`) pages the rows newest first. They are kept for
7 days.

**Content capture.** `gregale ai policy <id> --request-log-content on`
(`request_log_content`, which needs the request log) also keeps, for each
logged call (ADR-996):

- the first 32 KiB of the request body the gateway sent to the provider;
- the first 32 KiB of the response body it read back, with its
  `Content-Type`.

On a translated route these are the provider's formats, not the caller's. A
request body over 4 MiB is streamed unbuffered and stored empty, marked
truncated. At most 1,000 calls per integration and UTC day are captured;
later calls are logged without content.

`gregale ai logs show <id> <request-id>` (or
`GET .../ai-requests/{request-id}/content`) prints both bodies, as text or
base64. `gregale ai logs` marks captured calls in its CONTENT column.

Captures are kept for 3 days. Turning capture off deletes them within an
hour, and deleting the integration deletes them at once. They are stored
unencrypted, like app logs, so do not turn capture on for traffic you may not
store. E-mail addresses, payment cards, IBANs and secrets in captures are
always replaced with `[REDACTED:kind]` (ADR-997).

## AI unmetered calls

A hard cap holds only for calls the gateway meters, so a write on an AI
integration goes through only when the gateway meters its endpoint or knows it
to be free (ADR-1432).

- **Metered:** chat completions, Responses, completions, embeddings, images,
  audio, batches, and Veo video generation.
- **Known free:** file uploads, token counting, OpenAI moderation,
  cancellations, conversation items and stored-completion updates. The ADR
  lists them per provider.
- **Refused:** any other `POST`, and every `PUT` or `PATCH`, with
  `403 outbound_ai_endpoint_unmetered`. Examples:
  - realtime client secrets and calls, which let a browser spend with the
    provider directly (realtime WebSocket sessions are metered; see AI
    realtime sessions);
  - vector stores and containers, billed by storage and session;
  - fine-tuning jobs (OpenAI is winding its fine-tuning platform down);
  - Gemini context caches and batch mode, and Vertex batch and tuning jobs;
  - Bedrock `async-invoke`;
  - any endpoint a provider adds later.
- **Unchanged:** reads (`GET`, `HEAD`) and `DELETE`.

`gregale ai policy <id> --allow-unmetered on` (or `"allow_unmetered": true`)
lets refused calls through as before. They carry `X-Gregale-AI-Unmetered:
allowed`, their spend is outside the integration's caps, and
`outbound_ai_unmetered_calls_total` counts them by integration. Operator
integrations take `ai_allow_unmetered` in `outboundd.toml`.

## AI realtime sessions

OpenAI and Azure OpenAI Realtime WebSocket sessions pass through the gateway
metered (ADR 20261019072722). An app connects to
`ws://…/i/<integration-id>/v1/realtime?model=<model>` (Azure:
`/openai/v1/realtime` or `/openai/realtime`) through `GREGALE_OUTBOUND_URL` or
an access token, without a provider key.

- **Transparent.** outboundd forwards the caller's handshake with the
  provider credential, refuses compression, and copies frames unchanged both
  ways. It reads only the provider's text messages, up to 4 MiB each.
- **Per-response caps.** The upgrade is admitted like a call, with no
  reservation. Each response the provider starts (`response.created`) is
  admitted with its own reservation: the previous response's input tokens (4,096
  for the first) plus 4,096 output tokens, priced as audio. `response.done`
  settles it from its usage. A refusal sends the caller an `error` event and
  closes the session with code 1008.
- **Transcriptions** (`conversation.item.input_audio_transcription.completed`)
  settle under the session's transcription model and count against caps from
  the next response.
- **Fail closed.** A provider message the gateway cannot read ends the session.
  A response without usage, and any response still open when a session ends,
  settles at its reservation. Sessions last at most an hour.
- **Gemini and Vertex Live** (ADR 20261020161811). A `gemini` integration
  takes `/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent`
  (or `v1alpha`), a `vertex` integration
  `/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent` (or
  `v1beta1`); the integration's allowed path prefixes must include `/ws`.
  - The model is `setup.model` from the caller's first message, which the
    gateway reads, within 10 seconds and up to 4 MiB, before forwarding it.
    A first message without a model ends the session with 1008.
  - Each model turn is admitted at its first output (`modelTurn`,
    `outputTranscription` or `toolCall`) with the same reservation as an
    OpenAI response, and settled from the turn's `usageMetadata`: its prompt
    tokens (the context the turn read) and its output, split into text, audio
    and image. Video frames price as image tokens; search grounding is a tool
    fee of the turn.
  - Provider messages are read from binary frames as well as text. A refusal
    closes the session with 1008 and the refusal code as the reason.
- **Not metered:** Live ephemeral tokens (`…BidiGenerateContentConstrained`),
  every other WebSocket path, realtime calls (WebRTC) and client secrets.
  They are refused with
  `403 outbound_ai_endpoint_unmetered` unless the integration allows unmetered
  calls. Routes refuse upgrades with `400 outbound_ai_route_realtime_unsupported`.
- **Content policies** that check requests, responses or moderation refuse
  sessions with `403 outbound_ai_realtime_policy_unenforceable`.
- **Audio prices.** Price-list entries take `input_audio_millicents_per_mtok`,
  `cached_input_audio_millicents_per_mtok` and
  `output_audio_millicents_per_mtok` (CLI keys `input-audio`, `cached-audio`,
  `output-audio`); each falls back to the matching text price. The built-in
  catalog carries them for realtime and audio models, and Chat Completions
  audio usage is priced with them too. Usage items report
  `input_audio_tokens` and `output_audio_tokens`.
- **Metrics:** `outbound_ai_realtime_sessions_total{event}` (opened,
  unmetered, closed_client, closed_provider, closed_cap, closed_duration,
  closed_unreadable, closed_error) and `outbound_ai_realtime_sessions_active`.

## AI built-in tool fees

Providers bill some built-in tools per use, on top of tokens. The gateway
counts these uses from each response, prices them, and reserves them before the
call (ADR-1433).

| Tool | Counted from | Typical price |
| --- | --- | --- |
| OpenAI web search | Responses `web_search_call` items, or one per chat completion on a search model | $10 per 1,000 calls, $25 for `web_search_preview` on non-reasoning models |
| OpenAI file search | Responses `file_search_call` items | $2.50 per 1,000 calls |
| OpenAI code interpreter | Responses `code_interpreter_call` containers | $0.03 per GB per 20-minute session |
| Anthropic web search | `usage.server_tool_use.web_search_requests` | $10 per 1,000 searches |
| Gemini Google Search | `groundingMetadata.webSearchQueries` | Gemini 3 and newer: $14 per 1,000 queries; Gemini 2.5: $35 per 1,000 grounded prompts |

- **Prices.** The catalog prices web search per model, and file search and
  containers on OpenAI and Azure OpenAI models. A price list overrides them
  with `web-search`, `file-search` and `code-session-gb`:

  ```sh
  gregale ai prices set <id> --price model=gpt-5,input=1.25,output=10,web-search=0.01,file-search=0.0025,code-session-gb=0.03
  ```
- **Reservation.** A request that declares a tool reserves its fee before
  admission, so a cost cap holds ahead of the call:
  - web search: Anthropic's `max_uses`, else 5 (one for a chat search model
    or a Gemini 2.5 grounded prompt);
  - file search: 2;
  - code interpreter: one session at the container's `memory_limit` (default
    1 GB).
- **Containers** are charged once per 20-minute session window per outboundd
  process. A conversation that reuses a container is not charged again within
  the window.
- **Not covered:**
  - free allowances (Gemini's daily or monthly free grounding), which the
    gateway charges like the rest;
  - Anthropic code execution, billed by container hour;
  - Google Maps grounding.
- **Search content tokens** are in the providers' token counts and priced as
  tokens. With OpenAI's generally available `web_search` tool on a
  non-reasoning model, the catalog's preview price overstates each search
  until a price list corrects it.

`gregale ai usage` shows web searches, file searches and GB code sessions in
its MEDIA column. `outbound_ai_tool_uses_total` counts them by integration and
tool.

## AI media metering

See [ADR-997](../adr/997-ai-gateway-media-metering-content-guardrails-replay.md).

Image, transcription and speech calls count toward token and cost budgets
like chat calls:

- **Images:** OpenAI and Azure `images/generations`, `images/edits` and
  `images/variations`, and Imagen `predict` on Gemini and Vertex AI.
  gpt-image models report image tokens. DALL-E and Imagen are billed per
  image.
- **Transcription:** `audio/transcriptions` and `audio/translations`, billed
  per audio second (Whisper) or in tokens (gpt-4o-transcribe).
- **Speech:** `audio/speech`, billed per input character (tts-1) or settled
  at its reservation when the model has no character price.
- **Video:** Veo `predictLongRunning` on Gemini and Vertex AI (ADR-1432),
  billed per second of generated video at the price for its resolution.

Price lists take media prices next to token prices:

- `input-image` and `output-image` per million image tokens;
- `image` per image;
- `audio-minute` per minute of input audio;
- `characters` per million characters;
- `video-second` per second of video. A `model=<model>@<resolution>` entry,
  such as `veo-3.1-generate-001@4k`, prices one resolution apart.

For example:

```sh
gregale ai prices set <id> --price model=dall-e-3,image=0.04 \
  --price model=whisper-1,audio-minute=0.006 --price model=tts-1,characters=15
```

The built-in catalog carries these prices for the image, transcription and
speech models LiteLLM lists, and Veo's per-second prices by resolution.
`gregale ai usage` shows images, audio seconds, characters and video seconds in
its MEDIA column.

Reservations use the unit a model is priced in:

- **Images:** the requested image count (`n`, or Imagen's `sampleCount`). A
  token-priced model reserves 8,192 output tokens per image.
- **Transcription:** the upload's estimated duration at 16 kbit/s.
- **Speech:** the input characters.
- **Video:** `durationSeconds` (default 8) times the videos asked for
  (`numberOfVideos`, or Vertex's `sampleCount`, default 1), at `resolution`
  (default `720p`). A successful creation settles at that amount: the provider
  bills the seconds it was asked for, and the operation it returns reports no
  usage. A video whose operation later fails is still counted.
  - **Unmetered video:** a request the gateway cannot read (over 32 MiB), whose
    parameters are out of range, or whose model has no price per second.
    Unmetered video is refused unless the integration allows unmetered calls.

## AI content guardrails

`gregale ai content-policy <id> --email redact --payment-card block --secrets
block --block-term 'Project Falcon'` (or `PUT .../ai-content-policy`) checks
every AI request before admission. Each detector can be `off`, `redact` or
`block`:

| Detector | Finds |
| --- | --- |
| `email` | e-mail addresses |
| `payment_card` | 13 to 19 digit card numbers that pass the Luhn check |
| `iban` | IBANs with a valid check sum |
| `secret` | OpenAI, Anthropic, AWS, GitHub, Slack, Google and Stripe keys, JWTs, bearer tokens and PEM private keys |

Blocked terms are matched without case.

The gateway checks the string values of JSON bodies, skipping data URLs and
long base64 payloads, and the text fields of forms:

- **Redaction:** a redacted finding becomes `[REDACTED:kind]` before the
  provider sees it. The response carries
  `X-Gregale-AI-Content-Redactions: email=1`.
- **Blocking:** a blocked finding or term gets `400
  outbound_ai_content_blocked`, which names the detector but never echoes the
  content.
- **Forms:** a form cannot be rewritten, so a finding to redact in a form
  blocks it.
- **Unreadable bodies:** with a policy on, a body the gateway cannot read is
  refused. That covers bodies over 4 MiB, including large audio uploads, and
  JSON that does not parse.

Responses are checked only by the policy's `response` section; see [AI
response guardrails](#ai-response-guardrails).
`outbound_ai_content_findings_total` counts request findings by detector and
action.

## AI response guardrails

`gregale ai content-policy <id> --response-email redact --response-secrets
block --response-terms block --response-moderation flag` (or `response` in
`PUT .../ai-content-policy`) checks what the model sends back before the
caller sees it (ADR-1435):

- **Detectors:** `email`, `payment_card`, `iban` and `secret` redact or block
  as on requests.
- **Terms:** `blocked_terms: block` refuses a response that contains one of
  the policy's blocked terms.
- **Moderation:** `flag` or `block` runs the policy's moderation check on the
  response, with every category but `prompt_injection`, and a prompt about
  text an AI produced. The check's settings stay stored while requests are
  not moderated (`--moderation off`).

**What is checked:** the generated text of successful generation and
transcription calls:

- OpenAI chat and text completions, Responses API messages and refusals, and
  transcriptions;
- Anthropic text blocks;
- Gemini and Vertex text parts;
- Bedrock Converse text and Bedrock invoke model output.

Tool call arguments, thinking and reasoning text, citations, embeddings,
images, speech, video and batch results are not checked. Neither are
replays or the gateway's own checks.

**Whole responses** (JSON or text) are read in full first, up to 4 MiB:

- Redactions rewrite the text and send `X-Gregale-AI-Response-Redactions:
  email=1`. The JSON is re-encoded, so key order and spacing can change.
- A block answers `422 outbound_ai_response_blocked`, or `422
  outbound_ai_response_flagged` after moderation. A flag sends
  `X-Gregale-AI-Response-Moderation: flagged=<categories>`.
- A failed moderation check under `--moderation-on-error block` answers `503
  outbound_ai_moderation_unavailable`. A response the gateway cannot read or
  that is over 4 MiB answers `502 outbound_ai_response_unchecked`.
- Gemini's `streamGenerateContent` without `alt=sse` is a JSON array, so it
  is checked whole and arrives at the end.

**Streams** (server-sent events and Bedrock event streams) keep streaming:

- The gateway keeps each choice's, output part's or content block's text and
  releases an event once 512 more bytes of that text have arrived, or the
  stream ends. Each scan reads 8 KiB back, so a finding split across events
  is caught.
- Redactions rewrite the held events. A block ends the stream with `event:
  error` and `{"type":"error","error":{"code":"outbound_ai_response_blocked",...}}`,
  which OpenAI's and Anthropic's SDKs raise, or a Bedrock exception frame.
  OpenAI-format routes send an OpenAI error event.
- With `--response-moderation flag`, the check runs when the stream ends.
  With `block`, the stream is held until the check passes, so it arrives at
  the end.

**Billing:** a blocked response's tokens are still billed by the provider and
settle as usual. A stream the gateway ended settles like an aborted stream.

AI calls no longer forward the caller's `Accept-Encoding`: the gateway
negotiates compression with the provider and reads plain bytes, so usage is
read from every response.

`outbound_ai_response_checks_total` counts checks by result (`passed`,
`redacted`, `flagged`, `error`, `unchecked` or `blocked`), and
`outbound_ai_response_findings_total` counts findings by detector and action.
The log-export line carries `response_check`, `response_findings` and
`response_moderation_categories`.

## AI replay

`gregale ai replay <id> <request-id> --target <anthropic-id>:claude-sonnet-4-5
--target <openai-id>:gpt-4o-mini` resends a captured call (`gregale ai logs`
shows request IDs) to up to five targets. It waits, then prints the original
call and each target side by side: status, tokens, cost, latency, time to
first byte and the start of the output. `gregale ai replay show <replay-id>`
reads a replay again. The API is
`POST .../ai-requests/{request-id}/replays` and
`GET /v1/outbound/ai-replays/{replay-id}`.

- **Translation:** captured OpenAI chat completions, Responses and embeddings
  calls replay on any provider. Other calls replay on integrations of the same
  provider.
- **Model:** a target on another provider needs a model.
- **Streaming:** replays never stream, so time to first byte is the time to
  the whole response.
- **Accounting:** replayed calls go through the targets' ordinary path:
  budgets, guardrails, rate limits and the content policy apply. They appear
  in usage under the caller `replay` and are never captured.
- **What is resent:** the redacted capture. A call needs a whole capture, so a
  truncated request cannot be replayed.
- **Limits:** at most 100 replays per integration per UTC day
  (`429 outbound_ai_replay_limit`). Replays are kept for 3 days.

## AI model canaries

`gregale ai routes canary <route-id> --position 2 --percent 10
--max-latency-increase 50` (or `PUT /v1/outbound/ai-routes/{route}/canary`)
sends 10% of the route's calls to its second target first (ADR-1340). The rest
of the route's order is unchanged, so a failing canary call still falls back.

- **Choice:** a call with `X-Gregale-AI-Session`, or else `X-Gregale-AI-User`,
  always gets the same answer for the route, so one conversation stays on one
  model. Other calls are chosen at random. A canary call carries
  `X-Gregale-AI-Route-Canary: 1`.
- **Counts:** while the canary is active, every settled attempt of the route
  adds to a per-minute row for its target: calls, errors (provider status 400
  or above), time to first byte of successful attempts, and cost. Gateway
  refusals (caps, open breakers, cooldowns) are not counted.
- **Rollback:** every 30 seconds outboundd compares the canary with the first
  target over the window (default 30 minutes), once the canary has `min_calls`
  attempts (default 50). It rolls the canary back when:
  - its error rate is worse by more than `max_error_rate_increase` points
    (default 5); or
  - its mean time to first byte is worse by more than
    `max_latency_increase_percent` (unset by default).

  A rolled-back canary keeps its settings and receives no more calls.
- **Commands:** `gregale ai routes canary <route-id>` shows both targets'
  counts; `promote` moves the canary target to the front of the route; `stop`
  ends the canary. Replacing a route's targets also ends its canary.

`outbound_ai_route_canary_calls_total` counts calls by the target tried first;
`outbound_ai_route_canary_rollbacks_total` counts rollbacks by reason.
Per-minute rows are kept for 48 hours.

## AI route strategies

`gregale ai routes set <route-id> --target <a> --target <b> --strategy latency`
(or `strategy` in `PUT /v1/outbound/ai-routes/{route}/targets`) picks the
target each call tries first (ADR-1434). The other targets follow in route
order as fallbacks.

- **`ordered`:** the default; route order.
- **`weighted`:** `--weights 70,30` gives each target, in order, a weight from
  0 to 100. The first target is drawn by weight. A call with
  `X-Gregale-AI-Session`, or else `X-Gregale-AI-User`, always draws the same
  target. A weight of 0 makes a target a fallback only.
- **`latency`:** the target with the lowest mean time to first byte goes
  first.
- **`cost`:** the target with the lowest mean cost per successful attempt
  goes first. With `--max-ttfb-ms`, targets slower on average go after the
  ones within it.

For `latency` and `cost`:

- **Data:** settled attempts over the last 15 minutes, from the per-minute
  rows canaries use. Rows are written for these routes even without a canary.
  The route query outboundd already runs for each call reads the sums.
- **Order:** targets with at least 10 attempts and an error rate of at most
  25%, best first; then targets with fewer attempts, in route order; then
  targets above 25% errors.
- **Exploration:** 5% of calls put a target with fewer than 10 attempts
  first, so every target keeps current numbers. A new route routes in order,
  apart from exploration, until its targets have data.
- **Cost is measured,** including cache hits and tool fees, not list prices.

An active canary still sends its share to its target first; the strategy
orders the rest. Replacing a route's targets, or starting, stopping or
promoting a canary, resets its numbers.
`gregale ai routes stats <route-id>` (or
`GET /v1/outbound/ai-routes/{route}/stats`) shows each target's attempts,
errors, mean time to first byte and mean cost.
`outbound_ai_route_calls_total{strategy,pick}` counts calls by how the first
target was picked: `ordered`, `weighted`, `best`, `explore` or `canary`.

## AI log export

`gregale ai policy <id> --log-export on` (or `"log_export": true` in the AI
policy) sends each settled call of an app to that app's log drains (ADR-1340).
Calls of Runs, replays and moderation checks are not exported.

- **Line:** one JSON line on stream `ai_gateway`, with the model, status,
  outcome, tokens, cost, duration, time to first byte, attempts, route and
  canary, end user and moderation result. It never holds prompts or
  completions.
- **OTLP:** OTLP drains also get `gen_ai.system`, `gen_ai.response.model`,
  `gen_ai.usage.input_tokens` and `gen_ai.usage.output_tokens` as attributes.
- **Path:** settlement writes the line to `outbound_ai_log_exports` in the
  transaction it already sends. The gateway that delivers the app's runtime
  logs reads lines at least 15 seconds old and queues them in each drain's
  durable sender, as source `ai-gateway` with the row ID as its sequence.
- **Delivery:** drain health, retries and dead letters cover these lines like
  runtime lines. A drain new to a gateway starts 30 seconds back, so an
  ownership handoff can repeat or skip that interval.
- **Retention:** lines are kept for 24 hours; a drain down for longer loses
  the older ones.

`outbound_ai_log_exports_total` counts lines written by integration.

## AI moderation

`gregale ai content-policy <id> --moderation block --moderation-kind
classifier --moderation-integration <check-id> --moderation-model gpt-5-nano
--moderation-categories prompt_injection,harassment` adds a model-based check
to the content policy (ADR-1340).

- **Kinds:**
  - `openai_moderation` calls OpenAI's `/v1/moderations` (default model
    `omni-moderation-latest`) on an OpenAI integration.
  - `classifier` asks any chat model, through the route translators, which
    categories apply.
- **Categories:** `prompt_injection`, `harassment`, `hate`, `self_harm`,
  `sexual`, `violence` and `illicit`. `openai_moderation` cannot check
  `prompt_injection`.
- **Actions:**
  - `flag` lets the call through with `X-Gregale-AI-Moderation:
    flagged=<categories>`.
  - `block` refuses it with `400 outbound_ai_content_flagged`, which names the
    categories but never the content.

  A passed check sends `X-Gregale-AI-Moderation: passed`.
- **Failures:** a check that fails (timeout, provider error, a reply without a
  verdict, a deleted check integration, a body over 4 MiB the gateway cannot
  read) sends `X-Gregale-AI-Moderation: error`. `--moderation-on-error block` then refuses the call with `503
  outbound_ai_moderation_unavailable`; the default `allow` lets it through.
- **What is checked:** the text of user, tool and input content, never system,
  developer or assistant turns, and at most 32 KiB of it, latest first.
  Responses are checked only with `--response-moderation`.
- **When:** after admission, so a call the caps refuse never pays for a check,
  and before the provider call.
- **Cost:** the check runs through the check integration in process, so its
  caps, rate limits and price list apply. It appears in usage under the caller
  `moderation`. Moderation and replay calls are never moderated.

`outbound_ai_moderation_total` counts checks by integration and result.

**Dashboard.** `deploy/grafana/ai-gateway.json` charts:

- calls, tokens and cost;
- time to first byte;
- budget rejections and alerts;
- route fallbacks and translations;
- key pool events.

## AI batches

OpenAI and Azure OpenAI `POST .../batches` and Anthropic
`POST .../v1/messages/batches` are AI calls (ADR-1431). Before ADR-1431 they
passed through unmetered, so batch spend skipped every cap.

- **Estimate:** each request counts its body bytes at 4 bytes per token, plus
  its output ceiling (`max_completion_tokens`, `max_output_tokens` or
  `max_tokens`, else 4,096 tokens for a generation call). It is priced at 50%
  of the price list, the providers' batch price.
  - Anthropic's requests are read from the creation body (at most 32 MiB).
  - For OpenAI, the gateway reads the input file from the provider
    (`GET .../files/{id}/content`, at most 256 MiB), so the integration must
    allow `GET` under that path.
- **Refusals:** the provider is not called when the batch cannot be sized or
  kept.

  | Response | Reason |
  | --- | --- |
  | `400 outbound_ai_batch_unreadable` | The input file or body cannot be read or parsed. |
  | `413 outbound_ai_batch_too_large` | The input passes the sizes above, or the estimate passes one reservation's bound. |
  | `429` | The reservation passes a cap, as for any call. |
- **Reservation:** the batch is admitted like a call, then kept in
  `outbound_ai_batches` until it settles. Admission counts open batches in the
  statement that already totals leases, so later calls see the batch's spend.
  - A provider refusal releases the reservation.
  - A creation response without a batch ID settles at the reservation as
    unknown usage.
  - A reservation the provider never confirmed is released after 60 minutes.
- **Settlement:** outboundd checks open batches every 60 seconds, the first
  time 60 seconds after creation. When the provider ends a batch (completed,
  failed, expired or cancelled; Anthropic `ended`):
  1. it reads the results file;
  2. it records each model's usage at the batch price, under the batch's
     caller and keyed by the batch and the model, so a retry counts once;
  3. it releases the reservation.

  A batch without a successful result settles as an error with no usage. A
  batch still open after 72 hours settles at its reservation as unknown
  usage. Settled rows are kept for 7 days.
- **Content:** the content policy checks Anthropic's inline requests like
  any body, but an OpenAI input file is only sized, never scanned. Batches are
  not moderated, and their results are not captured or exported.

`outbound_ai_batches_settled_total` counts settled batches by outcome.

## AI access tokens

An access token lets a caller outside Gregale act as one app on the AI gateway:
local development, CI or a notebook (ADR-1431).
`gregale ai tokens create --app my-api --name laptop --expires-in-hours 24`
(or `POST /v1/apps/{slug}/ai-access-tokens`) issues one.

- **Secret:** `ggai_` and 43 characters, shown once. Gregale keeps only its
  SHA-256 hash and its first 12 characters, to recognise it.
- **Use:** set the SDK's base URL to the public host, with
  `/i/<integration-id>` or `/r/<route-id>` (for OpenAI, append `/v1`). Put the
  token where the SDK puts its API key: `Authorization: Bearer`, `x-api-key`,
  `x-goog-api-key` or `api-key`.
- **Headers removed:** every such key header, a Gemini `?key=` parameter and
  every Gregale identity header, before the provider credential is added.
- **Scope:** the app's bound AI integrations and the account's routes, or only
  the integrations listed with `--integration`. A token call is the app's call:
  its bindings, guardrails, caps, content policy and usage apply.
- **Limits:**
  - 20 live tokens per app;
  - a lifetime of 1 to 2,160 hours (default 168);
  - a revoked token works for at most 30 more seconds (outboundd caches
    lookups);
  - `last_used_at` is updated at most once a minute;
  - expired and revoked tokens are deleted after 30 days.
- **Errors:**
  - `401 outbound_access_token_invalid`: missing, unknown, revoked or expired;
  - `403 outbound_access_token_not_allowed`: the integration is outside the
    token's scope;
  - `503 outbound_access_token_unavailable`: the lookup failed, or
    `OutboundAIAccessTokenLookupsMax` (8) lookups of uncached tokens were
    already running (ADR 20261027132936). Known and unknown tokens are
    cached apart, so unknown ones never push out known ones;
    `outbound_ai_access_token_lookups_total{result}` counts lookups.

**Operator setup.** The public listener is off by default.

1. Set `public_listen_addr = "127.0.0.1:8097"` in `/etc/faas/outboundd.toml`
   and restart outboundd. The address must be loopback; outboundd refuses
   others. The listener serves only `/i/` and `/r/` paths, with no metrics or
   readiness endpoints.
2. Set `faas_ai_gateway_public_enabled: true` for the `public_edge` role. It
   adds a Caddy site for `faas_ai_gateway_public_host` (default
   `ai.<domain>`, covered by the Origin CA wildcard) that proxies to
   `faas_ai_gateway_public_upstream`.
3. Add a proxied Cloudflare DNS record for the host.

## AI evaluations

`gregale ai evaluate <route-id> --position 2 --judge <id> --judge-model gpt-5
--canary-percent 10` (or `POST /v1/outbound/ai-routes/{route}/evaluations`)
evaluates a route target against the route's first target before any user
sees it (ADR-1431). It waits for the verdict; `gregale ai evaluate show
<evaluation-id>` reads it again and `gregale ai evaluate list <route-id>` lists
a route's evaluations.

- **Samples:** the latest captured calls of the first target's integration
  (default 20, at most 50):
  - with a 2xx status and a whole capture;
  - OpenAI chat completions or Responses calls;
  - sent by apps or Runs, not by replays, moderation checks or evaluations.

  The first target must be an OpenAI or Azure OpenAI integration with
  `request_log_content` on.
- **Candidate:** each sample replays on the target, with its model, without
  streaming, translated for its provider.
- **Judge:** the judge integration's model receives the request's user text
  (as moderation reads it) and both answers, in a random order. It replies
  `{"winner": "A"|"B"|"tie"}`.
- **Verdict:** each sample is a `win`, `tie`, `loss`, `candidate_error`
  (counted as a loss) or `judge_error` (counted as a tie). The evaluation
  passes when losses and candidate errors are at most `max_loss_rate` percent
  of the sample (default 20).
- **Canary:** a passing evaluation starts its canary, unless the route already
  has an active canary or the target changed since the evaluation started.
  `canary_started` says whether it did.
- **Failures:** an evaluation fails, without a verdict, with one of:

  | Code | Reason |
  | --- | --- |
  | `outbound_ai_evaluation_no_captures` | No sample matched. |
  | `outbound_ai_evaluation_unsupported` | The first target is not an OpenAI or Azure OpenAI integration. |
  | `outbound_ai_evaluation_target_unavailable` | The target's integration is gone. |
  | `outbound_ai_evaluation_judge_unavailable` | The judge's integration is gone. |
  | `outbound_ai_evaluation_judge_failed` | More than half the samples got no verdict. |
  | `outbound_ai_evaluation_interrupted` | It ran for more than 90 minutes. |
- **Cost:** candidate and judge calls run in process through their
  integrations, so caps, rate limits and price lists apply. They appear in
  usage under the caller `evaluation`. Four samples run at a time, and each
  call has 120 seconds.
- **Limits:** 20 evaluations per route per UTC day
  (`429 outbound_ai_evaluation_limit`). Evaluations are kept for 30 days.

`outbound_ai_evaluations_total` counts finished evaluations by result (`pass`,
`fail` or `failed`).

## Runs

When vmmd serves the relay, an outbound-enabled Run gets
`GREGALE_OUTBOUND_URL` pointing at guest-init's relay instead of the buffered
broker helper. Runs therefore stream and use routes like apps.

- The relay connection carries the Run's granted integrations, and
  `outboundd` checks the Run grant and lease on every request.
- Older vmmd or guest images keep the broker helper, so a mixed rollout keeps
  working.
