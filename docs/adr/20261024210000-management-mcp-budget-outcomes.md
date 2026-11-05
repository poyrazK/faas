# ADR-20261024210000: Read-only route-budget action outcomes

## Status

Accepted — 2026-10-24. Extends ADR-1252 and ADR-20261023130958.

## Context

A route-budget mutation can commit while its response is lost. Existing native
idempotency receipts allow the original guarded apply to recover that response
while the reviewed plan remains live. Once the plan expires, replay authority
and its mutation payload disappear; retained action history must not recreate
them. Operators still need positive evidence of historical commitment.

## Decision

Expose `GET /v1/apps/{slug}/route-budgets/receipts/{request_id}`, requiring a
canonical nonzero UUID, current ownership, read-surface scope/MFA and exactly
one default/named environment selector. Resolve current immutable app, project
and environment identities and match the stored receipt to them. PostgreSQL
uses a read-only repeatable-read transaction and account/app/request SQLC query;
MemStore holds its existing lock. Recheck current target identity in HTTP before
release. Return only request UUID, exact original SHA-256 request fingerprint,
immutable target identities and original budget/app-policy revisions/source.
Never return policy configuration/routes. Preserve preexisting fingerprint bytes
and receipts; no migration, entitlement or limit changes are required.

Add read-only `gregale_get_action_outcome` for retained route-budget action IDs.
Before every native write attempt, atomically persist an encrypted companion
`budget-outcome-lookups/` record containing only request UUID, fingerprint and
immutable history binding. Do not retain the mutation body in this reference.
It shares the existing history retention, global connection capacity and grant
namespace. Reconcile/prune it with history; OAuth revocation purges it. Preserve
unknown namespaces for N-1 readers, import references from still-live legacy
plans only, and fail closed on malformed/future snapshots or invalid bindings.
Expired legacy history without this reference remains unresolved.

Compare every native receipt identity, original request fingerprint, expected
revision transition, app-policy revision and intended inheritance source. A
match returns `verified_receipt` with only redacted revision/source metadata.
Missing, unavailable or mismatched evidence returns `unresolved`, which never
means non-commit. Other action kinds are unsupported. Current read consent,
account/API/app/environment identity, retained history, lookup binding and
storage availability are rechecked before result release. Never replay writes,
restore expired mutation payloads, renew TTLs, update recorded action phases or
claim current configuration/runtime health. Native completion stays unobserved.

Go, Node and Python native clients expose the new read. MCP catalog grows to
58 read/planning tools and 67 with writes enabled; eight prompts and eleven
resource templates remain. Native rollout/provider qualification stays separate
from portable API, store and protocol fixtures.

## Consequences

Tests cover MemStore and PostgreSQL receipt scoping, historical stability after
later edits, preserved hash compatibility and HTTP read-only authorization.
Both MCP protocols exercise positive receipt verification without replay.
Encrypted restart tests discard the expired original plan, retain only its
minimal reference, recover the historical receipt with writes disabled and
prove no durable/history changes. Absence, receipt mismatches, final consent,
identity/storage changes, legacy formats and retention pruning remain covered.
Pinned real Codex checks unattempted outcomes across restart/refresh with zero
model requests and zero native writes; positive commitment evidence is qualified
by native store/HTTP and MCP fixture tests, not claimed from that read-only smoke.
