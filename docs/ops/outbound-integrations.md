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

- Prices are millicents per million tokens; EUR 0.15 is 15,000.
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
- **Cache:** with `response_cache_ttl_seconds` set, an identical
  non-streaming JSON request (same body, caller and headers) is served from
  the cache when the provider returned `200`. A cache hit costs no tokens.
- **OpenAI stream usage:** the gateway turns on
  `stream_options.include_usage` for OpenAI streaming completions. It records
  the extra usage chunk, then removes it from the stream the caller sees, so
  these calls no longer count as unknown usage.

Larger bodies stream exactly as before, without these features.

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
about 700 models across the six providers, in EUR millicents per million
tokens. A call's model is priced from the integration's price list first, then
from the catalog for the integration's provider. Reservations and settlement
use the same price.

- The file records its LiteLLM source commit, generation date and USD-to-EUR
  rate. Prices are base-tier list prices; batch, priority, long-context tiers
  and negotiated discounts are not modelled.
- A dated or versioned model the catalog lacks is priced as its family
  (`gpt-4o-2024-08-06` as `gpt-4o`, `claude-sonnet-4-5@20250929` as
  `claude-sonnet-4-5`). Bedrock cross-region prefixes and ARNs are stripped.
- `gregale ai catalog <provider> [--model NAME]` (or
  `GET /v1/outbound/ai-price-catalog?provider=&model=`) shows the prices.
- To refresh the catalog, download LiteLLM's
  `model_prices_and_context_window.json` at a pinned commit into its own
  directory, run `python3 -I scripts/gen_ai_price_catalog.py` with that
  commit and the current ECB rate, and review the diff in a PR.

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
import { aiGatewayURL, aiGatewayHeaders, opaqueAIUserId } from '@gregale/sdk-node';

const client = new OpenAI({ baseURL: `${aiGatewayURL({ route: routeId })}/v1`, apiKey: 'unused' });
const user = opaqueAIUserId(email, process.env.USER_ID_SECRET);
await client.chat.completions.create(body, { headers: aiGatewayHeaders({ user, session: chatId }) });
```

Python offers `ai_gateway_url`, `ai_gateway_headers` and `opaque_ai_user_id`
from `faas_sdk`. `opaqueAIUserId` turns an e-mail address into a stable
`u_…` ID. Pass a secret so the ID cannot be traced back to the address.

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

Price lists take media prices next to token prices:

- `input-image` and `output-image` per million image tokens;
- `image` per image;
- `audio-minute` per minute of input audio;
- `characters` per million characters.

For example:

```sh
gregale ai prices set <id> --price model=dall-e-3,image=0.04 \
  --price model=whisper-1,audio-minute=0.006 --price model=tts-1,characters=15
```

The built-in catalog carries these prices for the image, transcription and
speech models LiteLLM lists. `gregale ai usage` shows images, audio seconds and
characters in its MEDIA column.

Reservations use the unit a model is priced in:

- **Images:** the requested image count (`n`, or Imagen's `sampleCount`). A
  token-priced model reserves 8,192 output tokens per image.
- **Transcription:** the upload's estimated duration at 16 kbit/s.
- **Speech:** the input characters.

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

Responses are not checked. `outbound_ai_content_findings_total` counts
findings by detector and action.

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

**Dashboard.** `deploy/grafana/ai-gateway.json` charts:

- calls, tokens and cost;
- time to first byte;
- budget rejections and alerts;
- route fallbacks and translations;
- key pool events.

## Runs

When vmmd serves the relay, an outbound-enabled Run gets
`GREGALE_OUTBOUND_URL` pointing at guest-init's relay instead of the buffered
broker helper. Runs therefore stream and use routes like apps.

- The relay connection carries the Run's granted integrations, and
  `outboundd` checks the Run grant and lease on every request.
- Older vmmd or guest images keep the broker helper, so a mixed rollout keeps
  working.
