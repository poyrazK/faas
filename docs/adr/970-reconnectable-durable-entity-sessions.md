# ADR-970: Reconnectable durable entity sessions

Status: Implemented in source; CI and native acceptance pending.

## Decision

Provide a separately gated owner-authenticated WebSocket endpoint over existing
entity invocation and a pure Node application-session helper. Persist session UUID,
sequence and closed tombstones with application state; derive stable replay IDs
from command identity. Socket lifetime is independent of durable session lifetime.

Route frames through the same authentication/MFA/scope and entity admission chain
as HTTP invocation, rechecking app WebSocket eligibility per frame. Acknowledge
only committed results. Keep one in-flight request per socket with bounded frame,
response, write, idle and connection budgets. Refuse browser Origin headers; no
owner credential belongs in browser code. Capacity is per API process.

The starter includes an ordered room counter and isolated state validation.
Client frame preparation captures JSON and acknowledgement parsing checks matching
identity and safe versions. Reconnect replays the persisted pending frame; replaying
open is not a current progress query. Retain closed identities within the explicit
32-session budget, rather than silently deleting ordering history.

This first version does not supply public/end-user socket authorization, broadcasts,
presence, resume cursor lookup, application-state migrations in the helper,
expiry/reclamation, fleet-wide connection quotas or hibernation.

## Validation

Author transport slot/origin/frame tests, authenticated API reconnect/admission
checks, SDK lost-acknowledgement and ordered-state tests, and packed-starter restore
validation. Extend focused CI; local tests/builds remain deferred. Native credential
revocation, multi-owner routing and restart qualification remain release gates.
See [session protocol and limits](../durable-entity-sessions.md).
