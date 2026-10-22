# ADR-1252: Durable MCP OAuth renewal recovery

## Context

A provider may rotate a refresh token before an observer can install the returned
grant. Retrying the original token after a crash can invalidate the connection.

## Decision

Each observer role stores one bounded renewal journal alongside its sealed OAuth
connection. An intent is persisted under the live observer revision and lease
before sending the refresh request. Returned credentials are sealed and saved in
a separate transaction, fenced by revision and operation UUID. Result recording
can finish after lease expiry; installation requires the current live lease and
an exact match with the journal result.

A new lease owner installs a saved result without repeating the exchange. Expired
saved access tokens retain their rotated refresh token and trigger a fresh renewal.
An unfinished exchange inherited from another lease requires reauthorization.
Lost responses and malformed successful responses are also treated as uncertain;
received provider rejection permits ordinary retry, except invalid_grant.

Reconfiguration with identical OAuth bindings preserves the journal. Reconnect or
disconnect clears it with the old connection. Saved results discarded by a
configuration change or deletion enter the durable retirement queue. Source-secret
changes allow a new exchange and fence late results by operation ID. Public API and CLI show only
recovery state and start time, never operation IDs, source digests or sealed results.

## Consequences

Saving a result and installing it can fail independently without losing a recorded
rotation. External issuance still cannot share a database transaction: crashes
before result persistence require reconnecting. Reconfiguration, app deletion and
source changes can fence a late response; this does not claim provider-side cleanup.
The journal is role-owned, with at most one result per role and existing token-size
limits. It does not introduce VM or scheduler ownership changes. Rollback refuses
unresolved exchanges instead of allowing old workers to replay them.
