# ADR 1031: Group-bound realtime worker credentials

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Issue opaque 256-bit `grw_` credentials through account-authenticated apid routes.
Each credential binds to one immutable consumer-group UUID, implicitly fixing the
endpoint and channel, and names its worker identity. It cannot authorize account
APIs, group creation, publication, subscription tickets or other groups. Group
status exposes this stable `group_id`. Deleting/recreating the same group name
creates a different UUID and does not restore credential access.

Grant explicit operation permissions: status, claim, renew, complete, retry,
dead_letters, replay, resync and delete. Worker credentials cannot issue, rotate
or revoke credentials. Account admin scope plus MFA is required for issuance,
rotation and revocation; metadata listing follows the existing app read scope.
Normal worker credentials omit dead_letters/replay/resync/delete. There are no
implicit permissions or wildcard scopes.

Serve worker-only routes at `/v1/realtime/workers/groups/{group_id}`. Authenticate
only Bearer `grw_` credentials, using the shared per-IP authentication-failure
limiter. Valid workers continue even when another invalid caller exhausted the
NAT's failure budget. The original account-key group API remains available.
Enforce the existing history preview, live account/app status, enabled endpoint,
realtime plan access and exact per-operation grants. Store failures fail closed.

## Transactions, rotation and leases

Store only the SHA-256 hash, returning plaintext once on issue or rotation. No
claim or worker-secret response uses generic response-caching idempotency. Never
log credentials, claim tokens or hashes. Rotation preserves credential ID and
permissions, changes the secret and increments the generation; the old secret
stops authorizing new operations once rotation commits. A revoked credential
cannot be rotated. Expired unrevoked credentials may be rotated by an admin.

Authenticate once to resolve scope, then recheck the current hash, permission,
revocation, expiry and live account/app/endpoint under the operation transaction.
All worker operations lock the group before the credential. Rotation/revocation
uses the same group lock, fencing operations whose authentication raced with it.
Already-authorized transactions can finish before revocation obtains that lock;
after revocation commits, further worker operations fail. This does not cancel
external side effects already being performed by a worker. Revocation remains
available when the endpoint or history preview is disabled.

Tag each worker claim with its stable credential ID. Even possession of a claim
token cannot let another scoped credential complete, retry or renew that lease.
Rotation keeps ownership so the same worker can use its newly rotated secret for
an existing unexpired claim. Expiration/revocation prevents further settlement
and renewal; normal lease expiry requeues/dead-letters outstanding work. The
account-admin API remains the recovery authority. Credential deletion/metadata
cleanup never cascades away message snapshots.

## Bounds and observation

Limits live in pkg/api/limits.go: 64 metadata records per endpoint, worker names
up to 128 bytes, a lifetime of 1 second–30 days (default 1 day), and 9 distinct
permissions.
On issuance, remove records expired/revoked more than 7 days ago before admission.
Otherwise inactive metadata consumes the cap. Group deletion removes its metadata.

Count committed worker operations and retain the latest use time and operation;
empty claims count, failed authorization/rolled-back work does not. Rotation
preserves cumulative usage. Account-only inspector views show this metadata,
expiry, generation and revocation status, never plaintext or hashes. Destructive
worker actions and credential administration emit payload-free audit evidence.

The Node client holds authentication in a per-client config and calls a fresh
credential provider for every request. It does not modify global OpenAPI auth or
inherit account headers, basic auth or cookies. Its processing helper reuses the
lease/ACK flow and aborts the handler on renewal failure; clients must still use
idempotent effects and respect cancellation. Generated Node/Python clients expose
individual operations. Delivery remains at least once.

Apply 20261012102700473_realtime_worker_credentials.sql before deploying the
changed group writer: even account-key claims now write credential ownership.
No memory-store capability is fabricated. Runtime tests, DB concurrency checks,
migration execution and UI rendering remain deferred at the user's request.
