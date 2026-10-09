# Caddy upstream for `gatewayd-public`

Production terminates public TLS in Caddy and sends plain HTTP to
`gatewayd-public` on loopback. The daemon trusts forwarding headers only when
the immediate peer is listed in `FAAS_TRUSTED_INGRESS_CIDRS`; the production
unit lists `127.0.0.0/8` and `::1/128`.

`faas-gatewayd-public.socket` owns that loopback port. The socket stays open
while the daemon drains and restarts, so Caddy connections wait in a 4096-entry
kernel backlog until the new process consumes the inherited descriptor. Keep
the socket enabled with the service; binding port 8080 directly bypasses the
zero-connection-refusal rollout contract (issue #607 / ADR-068).

Private runtime-upgrade verification has a default-off public listener identity
and a read-only selected Caddy handler collector (ADR-702). The collector requires
an explicit loopback admin URL, a reviewed `/config/apps/http/servers/...` handler
path and exact expected startup identities for every static loopback backend. It
reads the selected scope twice around direct signed backend probes. It does not
discover every route or verify DNS, and it does not modify Caddy or clear pending
withdrawals. There is no automatic collector or public CLI. Deployment units keep
the private identity flag disabled; see
[ADR-702](../adr/702-selected-caddy-public-edge-binding.md) for bounds and pending
native acceptance. A missing identity or unreachable socket remains unverified.

[ADR-703](../adr/703-whole-caddy-http-config-inventory.md) adds a separate whole
declared HTTP configuration collector. It includes sibling servers, catch-all,
nested and error routes, and the separate S3 proxy. Full binding review requires
the whole configuration digest and every reverse proxy path; unknown modules and
unaccounted services remain unverified. Collection reports configured listeners
and local matcher predicates. Native sockets, generated HTTPS/ACME routes,
complete host/domain coverage and authoritative DNS still require acceptance.
No daemon starts this collector automatically, and no inventory clears a pending
withdrawal or grants a retirement lease.

[ADR-704](../adr/704-private-cloudflare-dns-configuration-inventory.md) adds a
separate private Cloudflare configuration collector for an explicitly reviewed
zone ID/name, using a DNS Read token. It scans every configured record
twice, validates complete pagination and brackets the scans with zone-detail
reads. It preserves DNS-only origins, wildcards, IPv6, aliases and delegations;
other record types and opaque metadata remain represented by digests. Provider
configuration is not proof of served DNS, effective CDN/Worker routing, external
aliases, other zones or native sockets. There is no automatic collection, provider
write or retirement authority. Keep execution and private deployment flags off.

[ADR-705](../adr/705-selected-served-dns-and-parent-delegation.md) observes selected
exact DNS-only A/AAAA/CNAME RRsets and an explicitly reviewed parent delegation.
It queries every declared parent/child literal TCP endpoint without recursion,
repeats NS/SOA/selected answers and brackets them with fresh provider inventory.
Mismatched, unreachable, negative or unsupported scopes return no observation.
Proxied origins, wildcard expansion, DNSSEC/root-chain trust, external aliases,
recursive caches and native origin/listener/service identities still need separate
reconciliation. These selected observations grant no retirement or future lease.

[ADR-706](../adr/706-selected-native-origin-and-service-reconciliation.md) adds
private selected native Linux reconciliation. It pins local systemd invocations,
process start times, executable hashes, cgroup identities, assigned origin IPs
and held TCP socket inodes around fresh DNS/Caddy/backend identity observations.
Every selected A/AAAA value needs an explicit same-family native Caddy listener;
each selected backend needs a reviewed main-process service holding its socket.
Socket activation can retain other holders. This reports selected inventory links,
not effective Host/TLS routing, UDP/QUIC, all services, external reachability or a
future exclusion/fencing lease. Unrelated S3 paths remain visible and unverified.
No daemon starts this adapter; native acceptance is pending and flags stay off.

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

api.gregale.dev, *.gregale.dev, gregale.dev {
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
