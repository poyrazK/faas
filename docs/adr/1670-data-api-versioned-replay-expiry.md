# ADR-1670: Versioned Data API replay expiry

Status: Accepted

## Context

Deleting a legacy note receipt permits the same key to create another note.
Read-only reconciliation (ADR-1669) avoids accidental write replay, but receipt
retention alone cannot reject late retries once the receipt has gone. A separate
client timestamp is insufficient: a caller can retain the key and change that
timestamp. The database must derive a fixed deadline from the operation identity.

## Decision

Add append-only starter migration `0012_note_replay_expiry.sql`. Opt in through
`create_note_with_tags_once_v2` and `read_note_create_receipt_v2`. Keep legacy
functions, tables and retention behavior unchanged. Contracts are independent
namespaces; applications must retain the contract version with their original
API, identity, key and full payload and must not switch versions during recovery.
This slice implements note creation; immutable orders continue to use their
existing durable receipt contract.

V2 requires an RFC 9562 UUIDv7 with its 48-bit Unix millisecond timestamp and RFC
variant. The fixed replay deadline is that timestamp plus 86,400,000 milliseconds
(24 hours). Allow timestamps up to 300,000 milliseconds (five minutes) ahead of
the database clock. These bounds live in `pkg/api/limits.go` and are mirrored in
the SQL under a parity test. The allowance does not extend the deadline: a slow
client clock shortens its effective window, and an ahead clock can produce up to
24 hours plus five minutes of availability measured from acceptance. Do not
extend this version's window in place; doing so could revive already purged keys.
Use a new contract version for a different policy.

The writer derives subject from verified JWT claims and serializes on the v2
subject/key advisory transaction lock. Check the server wall clock before and
after acquiring the lock, after business work, and before returning. Expired
keys fail even when no receipt exists; errors roll back database changes. An
unexpired replay compares the exact original payload and returns the stored
snapshot. This contract covers database effects only, not external effects.

Store immutable request/response snapshots in a separate RLS-protected v2 receipt
table. Generate `expires_at` from the UUID, independently of receipt creation time.
Use SELECT policies for scoped writer/lookup and INSERT for the writer only;
there is no application UPDATE or DELETE policy. Restore the transaction-local
scope on success and error. An owner maintenance trigger rejects receipt updates
and deletion before expiry. This guards routine maintenance; the schema owner
can still bypass it by changing the schema or truncating the table.

The stable invoker lookup performs no write or advisory lock and works in a
READ ONLY transaction. A retained receipt can confirm the original response even
after expiry. A visible payload mismatch gives `PT409` /
`idempotency_payload_conflict`. A missing unexpired receipt returns zero rows. A
missing expired receipt gives HTTP 410 / `PT410` / `idempotency_key_expired`, which
reveals only the public timestamp policy, not another subject's receipt. The
writer always rejects expired keys, including keys with retained receipts.
Invalid UUID versions/variants give `PT400` / `idempotency_key_invalid`, and a
timestamp too far ahead gives `PT400` / `idempotency_key_in_future`.

Extend owner cleanup with explicit `--contract v2`; its default remains v1. V2
eligibility uses the generated deadline at or before both the supplied past
cutoff and database statement time. Preserve preview, bounded committed batches,
owner validation, row `SKIP LOCKED` and nonblocking operation-lock acquisition.
Cleanup removes snapshots only, never notes. A later replay of the purged key
still fails by its timestamp. In-flight operations cannot be purged while they
hold their operation lock; lookups may observe a receipt or its absence around a
concurrent purge, and both outcomes follow the same reconciliation rules.

Export `createReplayKeyV2()` from the Data SDK using Web Crypto randomness and
the current client millisecond time. Reject an invalid clock or unavailable
secure random source; do not add a timestamp override or insecure fallback.
Add the `replay_expired` error category with reconciliation guidance, matched by
the exact SQLSTATE/message pair, and an unresolved `replay_expired` reason in
`reconcileMutation`. Preserve original errors and never create a replacement key,
poll, or automatically retry. Expiry establishes that this key cannot execute
again under this contract; it does not establish whether a previous attempt
committed. Keep the original pending operation for business review.

The starter controller exposes explicit `replayContract: 'v2'`, uses UUIDv7 by
default in that mode, and preserves pending state on expiry. Its default remains
v1 for existing callers; the new browser example explicitly chooses v2. New
deployments still require migration, owner RPC grants, runtime refresh and type
regeneration. No platform API endpoint, signing key or connection pool is added.

## Consequences

Receipt cleanup no longer reopens an operation key in v2. Applications can bound
receipt storage while continuing to reject late retries. Clients must have a
reasonably accurate clock and preserve the original operation context; modifying
its timestamp creates a different key and provides no duplicate protection.
Changing identity or API similarly creates a different namespace. Database wall
time must remain trustworthy: a large backward clock jump or database restore
that loses committed receipts can invalidate replay assumptions. This is not a
global exactly-once guarantee across restores, arbitrary owner SQL or external
systems. No automatic cleanup scheduling or hosted rollout is introduced.

## Validation

SDK tests cover UUIDv7 layout, original timestamp, secure randomness, invalid
clocks, exact expiry errors, typed unresolved recovery and absence of automatic
retries. Native PostgreSQL/PostgREST acceptance covers concurrent creation,
payload conflicts, subject isolation, future skew, expired unused keys, protected
live receipts, read-only lookups, RLS scope restoration, cleanup skipping a held
operation lock, replay waiting across expiry, retained confirmation, purge followed
by late retry, and rollback after business work waits beyond the deadline.
Browser and packed-client tests cover explicit v2 routing and preservation of the
original immutable operation on expiry. Prior migrations remain unchanged.
