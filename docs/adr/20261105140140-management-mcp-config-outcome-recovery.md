# ADR-20261105140140: Retained configuration batch outcome recovery

Date: 2026-11-05
Status: Accepted
Related: ADR-20261103171917, ADR-20261105131434, ADR-20261022161757

## Context

An atomic environment batch journals its native value-free receipt together
with the configuration revision. A client can retry the exact write while its
MCP plan remains live, but cannot passively read the receipt after a lost
response and plan expiry. A config commit and an optional fresh restart are
separate operations. A receipt also needs to identify the original immutable
named environment: a scope slug can be deleted and recreated.

## Decision

Expose a read-only, app-scoped native receipt endpoint by original request UUID
and scope. It returns the existing value-free receipt, checks current ownership
and named-environment identity, and never replays the batch. MemStore and
PgStore implement an optional receipt-reader interface; unavailable, missing,
unbound or legacy named-environment receipts fail closed. New identity-pinned
requests can carry expected app and environment UUIDs; the native transaction
checks them before reading a prior receipt or mutating configuration. Named
identity checks are made under the environment lock. Legacy requests remain
valid without those fields.

Each new MCP batch also carries a 256-bit random receipt proof. The native
store retains only its digest and requires the original proof in a request
header before returning the receipt. This binds recovery to the reviewed
request even if another writer guesses the deterministic request ID. A proof
is not returned in a receipt, URL, action history or MCP response. Legacy
receipts without a proof remain readable without one where scope permits, but
cannot satisfy a protected lookup.

New MCP configuration plans use identity-pinned requests. The pin marker is
part of the saved plan, so older already-attempted plans retain their original
request body on retry and do not receive an invented post-expiry lookup. An
older control plane rejects the new identity fields before writing; the bridge
does not downgrade a pinned request silently.

Record bounded, connection-local `operation_env` history and an encrypted
outcome reference in separate additive namespaces. The reference stores the
original request UUID, private proof, expected revision, a digest of updated key names and an
immutable target and restart-intent binding. It stores no variable values or
replayable request. Persist history and reference atomically before the native
batch. Newer and older bridges preserve unrelated namespaces, and the existing
capacity, retention and grant-revocation mechanisms apply.

The existing read-only `gregale_get_action_outcome` verifies the native receipt
against the original target, request, revision and key digest under current
consent. Its output omits request IDs, names, values and digests. A verified
receipt confirms the historical config commit only. It reports whether a
restart was requested but leaves its status unobserved; it does not infer
current configuration, restart completion or health. Missing or mismatched
evidence stays unresolved. Recipes and reads never replay writes or renew
expiry. Native receipt retention and bridge action retention have independent
clocks, so an otherwise-retained lookup may be unavailable.

## Validation

Exercise native receipt reads and identity pins against memory and PostgreSQL,
app-bound read-only authorization, lost replies and failed restarts across
encrypted bridge restart and plan expiry, both MCP protocol versions, receipt
mismatches, current-grant and immutable-app checks, final read guards,
revocation, legacy retries and absence of private data or repeat mutations.
