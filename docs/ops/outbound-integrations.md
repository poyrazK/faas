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
`outbound_upstream_latency_seconds`. Operator integrations use their
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
See [ADR-242](../adr/242-customer-outbound-binding-intent.md).

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
See [ADR-244](../adr/244-customer-outbound-binding-route-policy.md).

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
[ADR-243](../adr/243-customer-sealed-outbound-credentials.md).

An account can create its own integration without an operator pre-provisioning
the origin. Before enabling this workflow, configure
`workload_identity_jwks_path` with vmmd's public signing keys and ensure the
`outboundd` service has the fleet age private identity. `apid` must have the
matching fleet age public recipient. The shipped systemd unit loads the private
identity as a systemd credential; the private key is never placed in the app or
its environment. A newly-created integration will not require an `outboundd`
restart, but changing its JWKS or systemd credential configuration does.

Create the integration with a fixed public HTTPS origin and the maximum routes
any attached app may use:

```json
{
  "name": "payments",
  "origin": "https://api.stripe.com",
  "allowed_methods": ["GET", "POST"],
  "allowed_path_prefixes": ["/v1/customers", "/v1/payment_intents"],
  "daily_request_limit": 10000
}
```

Send that to `POST /v1/outbound/integrations`, then set the provider header
value with `PUT /v1/outbound/integrations/{id}/credential`, and attach the
integration with `PUT /v1/apps/{slug}/outbound-bindings/{id}`. The creation,
credential, and binding calls require MFA and deploy-write scope. Origin DNS is
checked before it is stored and checked again on every new gateway connection;
private, loopback, and special-use destinations are rejected. The customer's
method/path policy is the integration-wide ceiling; each app binding can narrow
it further. Customer-created integrations have a fixed 10 requests/second,
burst 20, 10 concurrent requests, and 30-second timeout, with a maximum of 25
enabled customer integrations per account. The credential remains unavailable
until uploaded and the gateway fails closed if it is missing or revoked.

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
integrations.

Delete a customer-owned integration with
`DELETE /v1/outbound/integrations/{id}`. This permanently removes its sealed
credential, app bindings, and admission state. This endpoint cannot delete
operator-provisioned integrations. See
[ADR-246](../adr/246-customer-created-outbound-integrations.md).

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
keys accordingly. See [ADR-239](../adr/239-platform-held-outbound-provider-authorization.md),
[ADR-241](../adr/241-managed-outbound-route-policy.md), and
[ADR-245](../adr/245-public-destination-dialing.md).

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
Callers decide whether and how to retry. Gregale makes no automatic retries,
does not follow redirects, and does not transparently intercept encrypted
egress. Provider responses (including provider `429`s) pass through.
If a provider response is interrupted or exceeds the gateway's body cap after
headers have been sent, the gateway aborts the response stream. Callers must
treat the resulting read error as an incomplete response, not a successful
download of the received prefix.

The listener also serves `/metrics` and `/readyz` on port `8095` by default.
Daemon HTTP request metrics record bounded status classes (`1xx` through `5xx`),
request latency, readiness, and standard OTLP exporter health. The outbound
integration metrics described above use only operator-owned integration IDs or
the shared `customer_managed` label; they never include URLs or raw provider
status codes.
