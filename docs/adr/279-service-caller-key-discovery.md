# ADR-279: Guest verification of service-caller assertions

- **Status:** accepted
- **Date:** 2026-09-26
- **Related:** ADR-206, ADR-278

## Context

ADR-206 added an opt-in node-local signer and a short-lived assertion carried
from the service proxy to the target as `X-Faas-Caller-Assertion`. The
`pkg/servicecaller` verifier already checks EdDSA, issuer, audience, and token
time bounds, but guest workloads had no supported way to discover the node
public keys. Operators otherwise had to distribute keys out of band, defeating
the platform-minted identity path.

## Decision

Expose `GET /v1/service-caller-keys` as an unauthenticated, rate-limited,
public-only JWK Set. The response contains each currently published per-node
Ed25519 key and no account or node metadata. `Cache-Control` allows a short
five-second cache and requires revalidation; a verifier should also refresh
when it sees an unknown `kid`.
The endpoint does not enable assertion signing.

On key rotation, persist the retired public key for `servicecaller.MaxTTL` (30
seconds) and return it beside the new key during that grace period. This lets a
target verify an assertion minted immediately before a caller-node rotation.
After the assertion window, the old key is removed from the trust set.

`pkg/servicecaller` provides `FetchTrustedKeys` for bounded HTTPS key retrieval,
`TrustedKeysFromJWKS` for parsing, and the existing `Verify` operation for
signature, issuer, target audience, and validity-window checks. The target
audience is the platform-injected `FAAS_APP_ID`. Non-loopback HTTP, redirects,
oversized responses, unsupported JWK algorithms, duplicate key IDs, and
key/fingerprint mismatches fail closed.

## Consequences

- A workload can verify who called it without treating a forwarded identity
  header as authoritative.
- The header remains optional during rollout. Operators must enable
  `FAAS_SERVICE_CALLER_ASSERTIONS=1` on every possible source node before an
  application makes verified identity mandatory.
- Public-key exposure is intentional: these keys cannot mint assertions, and
  the endpoint reveals no tenant-specific information.
- The 30-second rotation grace is tied to the assertion maximum lifetime.
  Assertions remain audience-bound, so a valid token for one target is not
  accepted by a sibling service.

## Rejected alternatives

- **Put verification keys in workload environment at boot.** A workload could
  outlive a node key rotation and miss keys for newly added nodes.
- **Use an unauthenticated JWKS fetch URL supplied inside each assertion.**
  Trusting token-controlled URLs would let an untrusted request steer verifier
  network access and key selection.
- **Turn signing on globally with this endpoint.** Key publication and
  application verification need a deliberate fleet rollout; the endpoint
  must not silently change request headers or app behavior.
