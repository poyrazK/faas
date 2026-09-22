# ADR-206 · Verifiable caller assertions for internal service calls

- **Status:** accepted
- **Date:** 2026-09-22

## Context

The internal service mesh (ADR-167..170, ADR-196, ADR-197) authorizes a call by
resolving the caller from the source IP of its VM's MASQUERADE and checking
that caller and target belong to the same account. That identity is sound and
unforgeable *at the proxy*: the guest never supplies it, and
`ServiceProxy.guestRequest` strips every platform-owned header before stamping
its own verdict.

What the target receives is a header. A workload that wants to know who called
it has exactly one option: trust that `gatewayd-internal` set
`x-faas-app` honestly and that nothing between the proxy and the guest altered
it. For a same-node hop that is a reasonable thing to trust — the platform owns
both ends. It is still a materially weaker statement than "I verified a
signature", and it is the wrong shape for two things we want next:

- **Per-service authorization.** The mesh is blanket same-account. There is no
  way to express "only `public-api` may call `billing`". Any such rule needs an
  identity the *enforcing* side can check, not one it is handed.
- **Cross-node calls.** ADR-169's identity map is node-local by construction:
  a source IP is only meaningful on the box that NATed it, and foreign-node
  source IPs fail closed. A signed assertion is node-independent.

ADR-167 listed "mTLS between workloads" as an open follow-up.

## Decision

The proxy **mints** a short-lived assertion attesting the caller identity it
already verified; the target **verifies** it against a published key.

```
sub = <caller app id>      aud = <target app id>
iss = gregale.svc          exp = now + 30s        jti = uuidv4
account_id, caller_instance_id, caller_env
```

Signed EdDSA with a **per-host service-caller key**, so the `kid` identifies
the node that asserted it.

### Why proxy-minted rather than guest-presented

`pkg/workloadidentity` already mints guest-facing assertions over vsock, and
its credential is the listener association — a guest cannot request a token for
another app because the instance id never crosses the wire. It would work as a
caller credential.

It is the wrong seam here anyway. Making the *caller* present a token means
every workload must fetch, cache, and refresh one before it can talk to a
dependency — a code change in every app, on the hot path, to prove something
the platform already knows with certainty. The proxy has the stronger claim
(it observed the packet) and it is already in the path. Minting there keeps the
caller unchanged and the assertion strictly more trustworthy than anything a
guest could assemble about itself.

### Why not per-guest mTLS

ADR-167's follow-up said mTLS; this ADR deliberately does not implement it.
Terminating TLS inside every guest means cert material and a rotation lifecycle
in `guest-init`, for every runtime, on the request path — new failure modes
(expiry, clock skew, rotation races) in the component whose resume hook is
already the most delicate part of a restore. The transport boundary is already
owned by the vmmd bridge; what was missing was a *statement about the caller*,
not an encrypted channel. A signed assertion supplies that, reusing three
subsystems that exist and are tested, and adds no per-guest key material.

If a future requirement genuinely needs channel encryption between guests
rather than caller attestation, that is a separate decision and this ADR does
not foreclose it.

### Why a per-host key rather than the cluster key

`cluster_signing_keys` is Ed25519 and fleet-wide, but schedd holds the sealed
private key and `gatewayd-internal` loads verifier public keys only. Handing
the fleet private key to the daemon that serves customer traffic on every node
would widen that blast radius considerably for no gain here: an assertion only
needs to be trustworthy, not fleet-signed.

`compute_node_keys` (ADR-053) is per-node and looks like a fit, but is not one:
those keys are ECDSA-P-256, owned and registered by **vmmd**, and scoped to
CapacityReport signing. Borrowing them would mean a second consumer with a
different algorithm and a different daemon holding the private half.

So the mint key is a dedicated per-host Ed25519 keypair, provisioned exactly
like ADR-119's per-host internal-service key: a path with a sealed-blob option,
generated on first boot with a loud warning if absent. A compromised key
invalidates one node's assertions rather than the fleet's, and the `kid` makes
which node is affected obvious.

## Consequences

- A target can verify its caller independently of trusting a header, and the
  same assertion works for a cross-node call where source-IP identity does not.
- **Nothing consumes it yet.** This ADR covers minting and the verification
  library. Guest-reachable JWKS, a runtime helper, and `allow_callers` policy
  are follow-ups. Until those land the assertion is additive metadata, so
  minting is behind `FAAS_SERVICE_CALLER_ASSERTIONS` and off by default — an
  unconsumed signature on the hot path should cost nothing until something
  reads it.
- The 30 s TTL matches ADR-119's internal-token bound. It is short enough that
  replay needs a live position on the node bridge, and long enough to survive a
  wake hold without re-minting mid-request.
- Assertions are minted per call. Signing an EdDSA JWT is sub-millisecond, but
  it is not free, and the flag is what keeps it off the path of operators who
  have no verifier.

## Rejected alternatives

- **Guest-presented `pkg/workloadidentity` tokens.** Requires every app to
  change to prove what the platform already observed. See above.
- **Per-guest mTLS.** Cert lifecycle in guest-init for an encrypted channel we
  already have. See above.
- **Signing with the cluster key.** Puts the fleet-wide private key in every
  data-plane daemon.
- **Reusing `compute_node_keys`.** ECDSA-P-256, vmmd-owned, scoped to capacity
  reports; a second consumer would need a different algorithm and a different
  private-key holder.
- **Reusing `pkg/internalsvc` unchanged.** Its `sub` is a platform service name
  against a per-service allowlist, `aud` is the constant `gregale.internal`.
  A caller assertion needs an app-id subject and a per-target audience, so
  sharing the type would make both contracts vaguer.
- **Minting only when a policy exists.** Ties the audit story to the
  authorization story; an operator may want verifiable call records without
  restricting anything.
