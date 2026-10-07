# ADR-617: Whole declared Caddy HTTP configuration inventory

Status: accepted · 2026-10-07

## Context

ADR-616 verifies a selected proxy's explicit backend startup identities. That
selection can omit sibling servers, customer-domain catch-all routes, nested
routes, error forwarding, or the separate S3 gateway. A complete reviewed graph
is needed before those observations can inform native ingress reconciliation.

## Decision

Add private read-only `edgetopology.CaddyInventoryProbe`. Read `/config/` from an
explicit literal loopback HTTP admin endpoint; never discover an endpoint or use
environment proxies, redirects or Caddy mutation. Require bounded unique-key
JSON and one strong ETag. Inventory every declared HTTP server in deterministic
name order, preserving listener order and the original route/handler positions.
Walk both normal and error routes at server and subroute levels. Retain local
matcher-set host patterns, parent relationships, groups, terminal flags, handler
modules, static upstream addresses and digests. Positive host matcher sets are
local predicates, not computed effective domain coverage or routing decisions.

Support standard static loopback reverse proxies, subroutes, static responses
(including redirects and aborts), and header handlers. Catalog the S3 proxy's
plain HTTP timeout and health metadata through its digest. Bound the whole body
at 1 MiB, servers at 32, listeners at 128, routes at 512, handlers/matcher sets/
host patterns at 1024 each, nesting at 16 and names at 128 bytes. Keep ADR-616's
1 KiB path, 64 public backend, two-second identity and ten-second total budgets.
All bounds live in `pkg/api/limits.go`. Require canonical literal IP TCP listeners
and literal loopback upstreams. Reject unsupported fields, custom/unknown apps,
handlers or matchers, listener wrappers, named/invoked routes, dynamic upstreams,
response-handler graphs, TLS/custom upstream transports, duplicate listeners or
backends and over-limit graphs. Never prune apparently unreachable handlers,
terminal siblings or negative matcher branches as proof of safe exclusion.

`Collect` returns a complete supported declared configuration inventory only
after identical full-body digests and ETags at both ends of collection. Do not
return raw admin/TLS/storage/header/matcher values or response bodies that might
contain secrets; retain their effect through configuration digests.

`ObserveBindings` additionally requires the exact reviewed full-config digest
and exactly one review for EVERY reverse proxy path, including error forwarding
and unrelated services. Match every upstream against its exact reviewed public
startup tuple with ADR-616's stricter connection protocol. Repeated addresses
across proxy paths must name the same tuple; probe each unique backend freshly
and never cache success. Any missing, extra, conflicting, unsupported or
unresponsive binding returns no successful observation. An S3 or other service
cannot be skipped or relabeled into a public-process proof; it stays unverified
until an adapter can prove its separate scope. Empty-proxy configurations grant
no absence/fencing authority. Freeze caller review data before network reads.
Return binding observations as a separate type containing the exact verified
proxy paths and startup tuples; configuration inventory alone is not that proof.

## Consequences

Selected-handler evidence can now be compared with the whole supported declared
HTTP graph, and full-config changes invalidate a binding review. Equal bookends
still cannot exclude ABA changes or create a lease for future traffic. The API
reports configured listeners, not kernel-bound sockets or Caddy process identity.
Generated automatic HTTPS redirects/ACME handlers, native HTTP/3 listener state,
custom modules, complete multi-host inventory, authoritative DNS and bypass
origins remain outside this proof. There is no database publication, daemon/CLI
wiring, automatic collection, public Apply, withdrawal clearance, retirement
lease or dead-process receipt. Read-only inventories do not classify unrelated
services as safe to ignore.

Execution remains unavailable and private deployment flags remain disabled.
No PR, push, deployment, production daemon, forced disconnect or artifact
deletion is authorized. Native Caddy/DNS inventory acceptance, native Linux
amd64 KVM `test-metal` and final `leakcheck` remain pending before enablement.
Local fixture admin servers and HTTP probes are synthetic contracts. Next work
is authoritative DNS and explicit native inventory/service-scope adapters, then
host/socket/network fencing for unreachable startup sessions.

References: [Caddy config API](https://caddyserver.com/docs/api),
[HTTP route flow](https://github.com/caddyserver/caddy/blob/master/modules/caddyhttp/routes.go),
[subroute error forwarding](https://github.com/caddyserver/caddy/blob/master/modules/caddyhttp/subroute.go).
