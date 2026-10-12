# ADR-871: Scoped durable browser sessions

Status: implemented as an opt-in preview; native acceptance pending.

## Decision

A trusted application backend issues a short-lived opaque capability for one
immutable entity scope, session UUID, generation, exact Origin and explicit
command list. It authenticates and authorizes its end user before issuance.

Persist SHA-256 credential digests in the existing private durable bucket with
conditional creation and CAS revocation. Re-read the record and authenticate the
persisted issuer key before every frame. Return only the public WebSocket
subprotocol after authenticating the credential-bearing subprotocol offer.
No owner credential, cookie or query-string credential authenticates a browser
connection. The service-only transport continues rejecting Origin headers.

The existing per-account/entity/process limits and ordered committed
acknowledgements apply. Browser grants authorize only `open`, `message`, `close`
and `status`; host checks precede dispatch and receipt replay. Expiry and cohort
reclamation remain service operations. Normal entity admission and immutable
scope fencing apply to each invocation.

## Consequences

Revocation works across execution nodes through strongly consistent bucket
reads, with an extra read per frame. Bucket/authentication failures deny access.
An invocation admitted before revocation/expiry can still commit and acknowledge.
The expiry window is at most five minutes; active sockets need a new grant to
reconnect afterward. Grant validity uses server time, independent of guest
lifecycle ticks. Generation is selected by the trusted backend and checked by
the recoverable guest helper.

Records need a prefix-restricted bucket lifecycle cleanup rule and are excluded
from entity retained-byte accounting/export/restore. Operators must redact the
credential-bearing WebSocket protocol header from request logging. Production
enablement requires the independent host/private-GCS/browser acceptance gate.

See [browser session contract](../durable-entity-browser-sessions.md).
