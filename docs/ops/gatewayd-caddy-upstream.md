# Caddy upstream for `gatewayd-public`

Production terminates public TLS in Caddy and sends plain HTTP to
`gatewayd-public` on loopback. The daemon trusts forwarding headers only when
the immediate peer is listed in `FAAS_TRUSTED_INGRESS_CIDRS`; the production
unit lists `127.0.0.0/8` and `::1/128`.

Caddy must reduce the validated proxy chain to one address because the internal
gateway deliberately rejects ambiguous `X-Forwarded-For` values. For a
Cloudflare-fronted origin, configure Caddy with Cloudflare's current published
IPv4 and IPv6 ranges, strict right-to-left parsing, and `CF-Connecting-IP` as
the first client address source:

```caddyfile
{
	servers {
		trusted_proxies static <Cloudflare IPv4 and IPv6 CIDRs>
		trusted_proxies_strict
		client_ip_headers CF-Connecting-IP X-Forwarded-For
	}
}

api.gregale.dev, *.gregale.dev, *.apps.gregale.dev, gregale.dev {
	tls /etc/caddy/certs/gregale.pem /etc/caddy/certs/gregale.key
	reverse_proxy 127.0.0.1:8080 {
		header_up X-Forwarded-For {client_ip}
	}
}
```

Retrieve the ranges from `https://www.cloudflare.com/ips-v4/` and
`https://www.cloudflare.com/ips-v6/`. Refresh them before validating a release
and whenever Cloudflare announces a network change. The `header_up` rewrite is
load-bearing: it sends the single address Caddy resolved after checking the
immediate peer, so direct origin requests cannot select their own identity.

Validate and reload without interrupting established connections:

```sh
caddy fmt --overwrite /etc/caddy/Caddyfile
caddy validate --config /etc/caddy/Caddyfile
systemctl reload caddy
systemctl is-active caddy
```

Acceptance requires both paths:

1. A public HTTPS request through Cloudflare reaches a guest with
   `X-Forwarded-Proto: https`, one external `X-Forwarded-For` value, and the
   same `X-Faas-Client-Ip` value.
2. A direct origin request carrying forged forwarding headers reaches the
   daemon with Caddy's direct peer identity; gatewayd-public also rejects those
   headers when the caller does not match its configured ingress CIDRs.

## Cloudflare timeout envelope

The gateway marks its own request-budget 504 responses with
`X-Faas-Error-Code: request_budget_exceeded` and `X-Faas-Request-Id`. Cloudflare
Free may otherwise replace an origin 504 body with a generic page. For proxied
customer routes, deploy the marker-aware adapter in
[`deploy/cloudflare/public-timeout-worker`](../../deploy/cloudflare/public-timeout-worker/README.md)
and keep a DNS-only origin hostname outside the Worker routes. The adapter
reconstructs only marked Gregale timeouts; unmarked 502/504 responses remain
unchanged so CDN and origin failures are not misclassified.
