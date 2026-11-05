# ADR 20261103090000 · Synth routes accept only the scheduler; validate and cache close two bypasses

- **Status:** accepted.
- **Date:** 2026-11-03
- **Amends:** ADR-015 (the synth socket's DAC-as-auth), ADR-119 (internal
  service tokens on synth requests), ADR-091 (`validate` on upgrade
  requests), ADR-122 edge response cache (credentialed requests)
- **Related:** ADR-068/070 (the gatewayd-public / gatewayd-internal split),
  ADR-20261102081618 (compute nodes take app traffic)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A fifth edge-security review found:

1. **Public traffic reached the scheduler's synth routes.** gatewayd-internal
   serves `/v1/synthesize`, `/v1/invocations:dispatch` and
   `/v1/invocations:dispatch_batch` on the same mux as app traffic, on every
   Host, ahead of the app handler. Their design (ADR-015) treated the unix
   socket's permissions as authentication. Since the Tier A7 split,
   gatewayd-public proxies every public request into that socket, and it
   deliberately routes these three paths to the compute data plane on every
   host, because schedd itself posts to gatewayd-public's loopback listener
   (`gateway_synth_target: tcp://127.0.0.1:8080`). The only check on the synth
   side was ADR-119's internal-service token, required only for apps in
   `internal_only` mode. So a request from the internet to any app host,
   naming any app ID, could wake that app (billed to its owner) or invoke it
   with an arbitrary method, path, headers and body and read its response,
   skipping its public auth (bearer, basic, IP allowlist), every edge rule and
   the challenge, and posing as a cron, queue or async invocation.
2. **`validate` skipped any request that asked for an upgrade.** A request
   with `Connection: Upgrade` and any `Upgrade` value skipped the schema and
   the parameter checks unless the rule set `apply_while_streaming`. Any
   client can add the headers, and a server that does not speak the named
   protocol (Go's net/http, among others) runs the request as usual, body and
   all.
3. **The shared response cache served keyed responses to anonymous
   callers.** It skipped requests with `Authorization` or a cookie, but an API
   that takes its key in `X-API-Key` (or sits behind a proxy that uses
   `Proxy-Authorization`) had one caller's response stored and served to
   others unless the app marked it private.

The review also suspected that the cache key ignored the request host. It
does not: `computeVaryHash` folds the host into every key.

## Decision

1. **Every synth request carries a scheduler token for its app, and the
   gateway refuses one without it.** schedd mints its existing ed25519
   internal-service token (`sub=schedd`, `aud=gregale.internal`, 30 s,
   `app_id` claim) for every `/v1/synthesize`, `/v1/invocations:dispatch` and
   `/v1/invocations:dispatch_batch` request, whatever the app's mode. The
   synth server answers 403 (`instances.synth_unauthorized`, with the reason)
   unless the token verifies, names `schedd`, and its `app_id` is the app the
   request names. The check runs before the app's policy lookup, workflow
   admission and any wake. The token goes to the gateway only; the synth
   handler never forwards the HTTP `Authorization` header to the app. With no
   verifier configured (no cluster signing key and no
   `FAAS_INTERNAL_SVC_PUBKEYS`), nothing can be verified: the gateway logs a
   warning at boot and the edge refusal below is the only guard.
2. **The public edge refuses these paths unless the local scheduler sent
   them.** gatewayd-public answers 404 to a synth path unless the request
   came over plain HTTP from a loopback peer, with an IP or `localhost` Host
   and none of `X-Forwarded-For`, `X-Forwarded-Proto`, `X-Forwarded-Host`,
   `Forwarded`, `Via` or `CF-Connecting-IP`. Caddy and Cloudflare also reach
   that listener over loopback, but always add forwarding headers, and the
   compute edge's direct listener is TLS. A Host header alone proves nothing:
   Caddy's catch-all customer-domain site forwards whatever Host the client
   sends.
3. **`validate` checks parameters on every request, and the body of every
   request that has one.** Only a body-less upgrade request (a WebSocket
   handshake) skips the schema without `apply_while_streaming`.
4. **`X-API-Key` and `Proxy-Authorization` make a request credentialed for
   the shared cache,** like `Authorization` and cookies: it is neither served
   from nor stored in the cache.

## Consequences

- Release order matters for item 1: schedd (control plane) must send tokens
  before gateways (edge/node) require them, which is the documented deploy
  order. A gateway at this release beside an older schedd refuses that
  schedd's crons, async invokes and trigger batches for open apps until
  schedd is upgraded; an older gateway ignores the new tokens. Rolling schedd
  back past this release while gateways stay at it stops those deliveries.
- Apps cannot serve `/v1/synthesize` or the dispatch paths themselves. They
  could not before either: gatewayd-internal intercepted them.
- `apply_while_streaming` keeps its meaning for real streams; it no longer
  decides whether a body sent with upgrade headers is checked.
- Responses to keyed requests are no longer cached. An app that wants them
  cached per key needs a per-principal cache, which ADR-122 leaves out.

## Alternatives considered

- **A separate private listener for synth.** Cleanest, but production schedd
  dials gatewayd-public's loopback listener and relies on its compute
  routing; moving it is a deployment change across every split-box manifest.
  The token gives the same guarantee without it, and the edge refusal keeps a
  verifier-less install closed to the internet.
- **Requiring GET for every upgrade.** Would also close the validate bypass,
  but changes the raw forwarder for non-GET upgrades apps may use.
