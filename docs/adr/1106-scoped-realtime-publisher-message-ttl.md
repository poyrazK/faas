# 1106: Message TTL for scoped realtime publishers

## Decision

Scoped publisher messages accept `ttl_seconds` (0–86400, default zero). This is
message delivery lifetime; credential issuance and rotation `ttl_seconds` remain
token lifetime. Credential issuance accepts immutable `max_message_ttl_seconds`
(0–86400, default zero for uncapped). A positive cap requires each message to
explicitly supply positive TTL at or below the cap. Zero or omission cannot bypass
a cap. Rotation preserves policy; existing credentials default to uncapped.

Validate message TTL through the existing state append context. Check the cap
inside the publication transaction after locking and revalidating the credential,
before receipts, rate accounting, or publication. Invalid TTL bounds return the
existing invalid-request response; cap violations return the existing grant-denied
response. Credential policy errors create no credential.

Use retained append expiration persistence so the message, expiration row and
publisher receipt commit atomically. Positive TTL participates in content identity;
omit zero from the canonical hash to preserve existing receipt identities.
Receipt retries reconstruct expiry from original published_at and requested TTL,
even after retained history pruning. A changed lifetime conflicts. Receipt
retention remains bounded by the existing policy; after receipt expiry, a key can
be accepted anew according to the original publishing contract.

Carry committed expiry into live fanout. Existing retained WebSocket/SSE checkpoints
and REST suppression handle expired events without removing sequence positions.
Expiry does not retract in-flight payloads, reducer effects or consumer-group jobs.
Token expiry, rotation and revocation do not alter already accepted message TTL.

## Rollout and verification

Apply migration 20261012102700546 after retained-message expiry migration and
before updated apid. Deploy checkpoint-capable readers and SDKs before enabling
TTL publishing. The OpenAPI and Python/Node models expose both message lifetime
and credential cap. Static Go build, SDK compilation/type checks, schema parsing,
sqlc generation and whitespace checks only; runtime tests and migrations remain
deferred at the user's request.
