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
`outbound_upstream_latency_seconds`, all labelled only by configured
integration ID and bounded outcome/rejection vocabularies. The in-flight gauge
is per gateway process; the Postgres-backed admission decision remains the
authoritative fleet-wide limit.

Create `/etc/faas/outboundd.toml` from the Ansible example. Each integration
specifies a UUID, a fixed `https://` origin, attached app UUIDs, a rate/burst,
and `max_in_flight`. Put the raw Gregale gateway token in the named environment
file (`/etc/faas/secrets/outboundd/outboundd.env`); only its SHA-256 digest is
stored in Postgres. For integrations without a managed provider credential,
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

Managed integrations require a vmmd-signed workload identity assertion. Copy
the public JWKS published by the configured vmmd signer to a local file readable
by `outboundd`, then configure `workload_identity_jwks_path` and the exact
`workload_identity_issuer` used by vmmd. The daemon refuses to start a managed
integration without a valid local JWKS. Keep the old and new public keys in
that file during signing-key rotation, restart every `outboundd` replica, then
remove the old key after all assertions signed with it have expired. Drain
older gateway binaries before exposing a managed integration: they may still
accept the legacy shared token.

This is an operator-configured primitive. A bound app can use the provider
credential through the gateway, but the binding does not yet restrict provider
paths or methods, and an external provider could echo a credential in its own
response. Scope provider keys accordingly. See [ADR-239](../adr/239-platform-held-outbound-provider-authorization.md).

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

The loopback listener also serves `/metrics` and `/readyz` on port `8095` by
default. Prometheus records bounded request status classes (`1xx` through
`5xx`), request latency, readiness, and the standard OTLP exporter health
metrics. The request metrics intentionally do not include integration IDs,
URLs, or raw provider status codes as labels.
