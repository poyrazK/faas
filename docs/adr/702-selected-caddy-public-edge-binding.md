# ADR-702: Selected Caddy proxy to public process binding

Status: accepted · 2026-10-07

## Context

ADR-699 names reviewed public startup sessions and an opaque topology digest.
ADR-701 retains removed sessions until their permanent local fence drains. A
configured upstream address alone cannot identify the process now serving that
socket. The systemd public socket can remain open across process replacement.
DNS or Caddy route removal does not establish completion of existing streams.

## Decision

Add default-off `FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_IDENTITY=1`, requiring public
withdrawal, activity, confirmation, the same installed connection guard on HTTP
and upgrade paths, and the existing PostgreSQL stores. Bind this protocol to the
startup configuration digest. Install the signed handler on the actual public
HTTP listener only, after constructing these mechanisms. Reserve infrastructure
host `gatewayd-public.faas` and path `/v1/internal/runtime-public-edge/identity`;
the same customer path on other hosts retains its original behavior. Do not
install this handler on the control listener.

Use the existing private ingress secret with distinct public-edge request and
response HMAC domains. Each probe generates a fresh canonical UUID nonce; sign
the exact slot, startup session, configuration digest and nonce. Never send the shared
secret itself. Reject malformed, unauthenticated, duplicate or extra fields,
ambiguous request framing, oversized replies, redirects, and stale/replaced
startup tuples. A proof identifies a participating listener; it is not fresh
roster membership, a heartbeat, a drain receipt or an external fence.

Add a private read-only `edgetopology.CaddyProbe` with an explicit loopback HTTP
admin endpoint and reviewed selected-handler configuration path, including
nested subroutes. Read that scope via Caddy's GET config API. Require one strong ETag
and bounded unique-key JSON. Support only static canonical loopback IP TCP
upstreams, plain HTTP transport and the current deployment's header settings.
Reject unsupported proxy fields, dynamic upstreams, placeholders, hostnames,
TLS/custom transports, duplicates, empty/over-limit lists and any extra or missing
expected backend. Bounds live in `pkg/api/limits.go`: 64 edges, 64 KiB selected
config, 1 KiB path, 10 seconds total; each identity probe keeps ADR-698's two
second/1 KiB bounds. No environment proxies, DNS resolution, pooling, automatic
redirects, customer forwarding or Caddy mutation.

Probe every configured backend directly from the Caddy host against the exact
expected startup tuple. Then read the same config scope again and require equal
bytes/digest and ETag. Return only a complete point-in-time observation containing
the path, digest, explicit bindings and local check time. Every failure discards
partial success. Equal bookends do not detect all ABA changes or bind later
customer traffic, and they never establish an exclusion lease.

## Consequences

A reviewed Caddy handler can now be checked against the actual participating
public startup processes rather than a socket address or an operator hash alone.
This deliberately verifies a selected config scope, not full host routing,
wildcard/customer-domain coverage, all Caddy instances, authoritative DNS, bypass
origins, raw TCP/UDP, or native topology completeness. It neither publishes
database facts nor replaces ADR-699's opaque topology review. No daemon or CLI
starts this collector automatically. An unreachable backend remains unverified;
no dead-process receipt is fabricated and no ADR-701 intent is cleared.

Next steps are authoritative DNS and complete native ingress inventory adapters,
then host/socket/network fencing for unreachable sessions. Removing a proxy or
DNS record alone cannot prove that existing connections ended.

Private flags remain disabled in deployment units; `execution_available=false`.
No PR, push, deployment, production daemon, disconnect, retirement or artifact
deletion is authorized. Native Linux amd64 KVM `test-metal` and final `leakcheck`
remain pending before enablement. Local HTTP and fixture admin-server tests are
synthetic contracts, not native Caddy/DNS/KVM acceptance.

References: [Caddy GET config and ETags](https://caddyserver.com/docs/api),
[Caddy reverse proxy](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy).
