# ADR-1260: Authorization-bounded management MCP request leases

## Status

Accepted. Extends ADR-1254 and ADR-1258 through ADR-1259.

## Decision

For every authenticated remote MCP request, snapshot the earliest expiry of its
presented access token, OAuth grant and nonzero native-credential expiry under the
bridge lock. Reject an already expired lease before account verification. Bound
verification by both that lease and the configured API timeout.

After native account verification, recheck exact token-to-grant membership,
revocation and all expiries under the lock. A rotated token, replaced grant or
expired credential cannot dispatch a handler after verification. Narrow an
existing lease if the current expiry is earlier; never extend its captured deadline.

Attach the lease deadline and its fixed private context value to HTTP dispatch.
Reapply the deadline in SDK receiving middleware for every RPC, because legacy
stateless HTTP clients detach ordinary request cancellation while retaining context
values. Tools, reads, prompts, completions, rollout watches and subscriptions share
the same bound. Local stdio retains its existing timeout/lifecycle behavior.

Expiration cancels native reads and active handlers and releases reservations after
handler cleanup. No later request, pruning pass or periodic expiry worker is needed.
Token refresh can authorize later requests but cannot extend an existing request
that started under the old token. Access expiry does not automatically revoke a
still-refreshable grant. Subscription lifetime/resource-count bounds remain in force.

## Consequences

Admission denial uses the existing OAuth 401 challenge. In-flight handlers use their
existing cancellation/timeout contracts; completion does not guarantee that an
accepted native mutation was undone. Preserve durable attempt/retry state after
request expiry, then retry the same plan ID with fresh authorization where the
grant and plan remain valid. Expired grants or native credentials require renewed
consent/credentials rather than extending a running request. Journal cleanup may
finish after the authorization deadline to preserve retry safety; no new customer
API operation is authorized by that cleanup.
