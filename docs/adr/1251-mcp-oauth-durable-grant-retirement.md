# ADR-1251: Durable MCP OAuth grant retirement

## Context

A role-owned revocation record disappears when its application is purged. OAuth
configuration replacement and successful reconnection can also discard the prior
grant, while a provider may issue a new grant before local installation fails.

## Decision

Account-owned retirement records have no application foreign key. Monitor edits
atomically enqueue the previous sealed grant and install or clear local credentials.
A database deletion trigger captures active grants and sealed client credentials
before app tombstones or physical removal; the memory store mirrors tombstones.
Deleted app restoration requires reconnecting because the old local grant and
pending callback are removed and the role revision changes.

Code exchanges reserve durable retirement capacity before contacting the provider.
Every observed token is sealed and persisted before scope validation and installation.
Installation checks account, app, role, binding, exact sealed grant and absence of a
live retirement lease, then clears the reservation's sealed copy in the monitor
transaction. Failed installation leaves the issued grant available for retirement.
A ten-second installation grace avoids racing ordinary callbacks with the worker.

External issuance cannot join a database transaction. A lost reply, crash before
recording observed tokens, or unavailable persistence is reported as unknown issuance
rather than verified cleanup. Such records require provider-side investigation or
manual revocation. One-time authorization codes are never stored for replay.

The worker uses the durable table independently of app existence. Matching active
connections and in-flight exchanges hold automatic retirement because RFC 7009 may
invalidate an entire provider grant. This includes paused connections and other apps
in the same account. An administrator may explicitly authorize retirement despite
active connections. New exchanges cannot start while the same binding has pending
retirement. A live retirement lease prevents installation of its grant.

Failed retirement retains sealed data for recovery, bounded to seven days. Retrying
resets the eight-attempt budget while preserving phase acknowledgements. Manual
acknowledgement and provider acknowledgement clear sealed values. Retention expiry
also clears them; metadata expires after thirty days. Account outstanding records
are capped at 64, and retirement action history at 32 entries. Capacity exhaustion
rejects the local mutation before credentials are lost or issuance begins.

Administrator-only account history and recovery endpoints remain usable after app
deletion. Recovery follows the existing MFA policy. All public projections remove
sealed grants, frozen provider bindings and lease tokens. CLI recovery requires an
explicit flag to allow active-grant revocation; this can invalidate the replacement.

## Consequences

App deletion and connection replacement preserve retirement intent and ciphertext.
The existing apid-owned background worker remains responsible for provider calls;
no VM lifecycle or scheduler ownership changes are introduced. Legacy role progress
is a public compatibility projection of the canonical account-owned record.

Provider acknowledgement includes already-invalid tokens. Manual acknowledgement
records an operator assertion, not provider evidence. Missing endpoints, unknown
issuance and expired credentials can require provider console recovery. Account
removal still cascades account-owned records. Rollback refuses while retirement
records remain, preserving the operator's opportunity to export metadata first.

Catalog connection state, OAuth grant retirement records and edge-rule replay
ownership are operational, account-specific state. Environment clones start
without these records; their connections require independent configuration and
authorization rather than copying private grants or replay history.
