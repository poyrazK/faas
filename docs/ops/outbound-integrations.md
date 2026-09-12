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

Create `/etc/faas/outboundd.toml` from the Ansible example. Each integration
specifies a UUID, a fixed `https://` origin, attached app UUIDs, a rate/burst,
and `max_in_flight`. Put the raw Gregale gateway token in the named environment
file (`/etc/faas/secrets/outboundd/outboundd.env`); only its SHA-256 digest is
stored in Postgres. Provider authentication remains application-owned:
non-`X-Gregale-*` headers such as `Authorization` are forwarded.
Provision the same gateway token to each attached app as a secret; it is not
derived from or logged by the gateway.

An application calls the gateway explicitly. For example:

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
