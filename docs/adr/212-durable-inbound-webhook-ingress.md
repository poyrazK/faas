# ADR-212 · Durable inbound webhook ingress

- **Status:** accepted
- **Date:** 2026-09-22
- **Decision:** Accept provider-signed webhooks at an app-scoped Gregale URL,
  persist a deterministic invocation receipt before returning `202 Accepted`,
  and let the existing scheduler wake and deliver to the app.
- **Why:** A scale-to-zero app cannot receive a provider callback directly.
  Provider timeout windows should not determine whether a sleeping app loses an
  event, and provider retries should not create duplicate app deliveries.

## Contract

An authenticated customer creates an inbound webhook endpoint for an app. The
create response reveals an opaque `gwh_...` URL once. Gregale stores only its
SHA-256 digest. The provider signing secret is age/X25519-sealed at rest and is
never returned; management responses expose only a masked constant.

The public ingress performs these steps in order:

1. Resolve the opaque route token by digest and require an enabled endpoint.
2. Read at most 1 MiB and verify the provider signature over the exact raw body.
3. Extract the provider's stable event ID.
4. insert a pending invocation whose UUID is derived from endpoint ID and
   provider event ID;
5. return `202 Accepted` only after that insert commits.

An existing row is a successful duplicate receipt, not an error. The response
returns the same receipt ID with `duplicate: true`. This makes provider retries
idempotent even when the first acknowledgement was lost in transit.

The invocation row is the durable inbox record. Its payload is the provider
JSON, its target is the endpoint's configured app path, and metadata headers
identify the provider, endpoint, and event. Its dedicated `inbound_webhook`
source becomes the platform-owned `X-Faas-Invocation-Source` guest header and
is the authoritative delivery marker. The original provider signature is not
forwarded: it may be stale by the time a sleeping app wakes, and replaying an
external credential inside the platform would conflate ingress trust with app
delivery trust.

The existing scheduler drain owns everything after acceptance: wake admission,
delivery, configured retry, completion, and dead-lettering. `apid` records
customer intent only; it does not wake instances or transition invocation
state. This preserves the apid/schedd boundary.

The first provider adapter is Stripe (`Stripe-Signature`, v1 HMAC, five-minute
ingress tolerance). Additional providers require an explicit closed-set adapter
and schema migration; a generic unsigned endpoint is intentionally excluded.

## Consequences

- The app can be asleep while its public webhook endpoint remains available.
- A database outage fails closed with a non-2xx response, allowing the provider
  to retry; Gregale never acknowledges a memory-only receipt.
- Delivery inherits the normal async invocation concurrency, retry-policy, and
  DLQ behavior.
- Deleting an endpoint stops new ingress but does not delete already accepted
  invocations.
- Endpoint URLs cannot be recovered from storage. Losing one requires creating
  a replacement endpoint and updating the provider.

## Rejected alternatives

- **Proxy directly after waking the app:** keeps the provider connection open
  and couples acceptance to cold-start latency.
- **A second inbox/delivery state machine:** duplicates the durable invocation
  queue, scheduler wake path, retry policy, and DLQ.
- **A generic shared-secret or unsigned endpoint:** weakens provider-specific
  verification and offers no stable external event ID for deduplication.
- **Return `202` before the database write:** loses events on process or host
  failure and breaks the availability claim.
