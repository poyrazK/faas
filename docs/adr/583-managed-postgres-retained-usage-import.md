# ADR-583: Managed PostgreSQL retained usage import

Status: accepted · 2026-10-05

## Context

ADR-582 identifies missing accounting coverage, but automatic collection cannot
recover unavailable provider history or corrections outside its replay horizon.
An operator needs to repair canonical windows without bypassing admission,
inventing shutdown evidence, or silently replacing newer observations.

## Decision

Add operator-only preview and apply endpoints under
`/v1/admin/managed-postgres/accounting/{account_id}/usage-imports`, with preview
at `/preview`. Preview uses the existing operator/admin/session-MFA read policy
and does not write. Apply uses `requireAdminMutation`: an allowlisted operator
session, recent MFA step-up, same-origin checks, and an Idempotency-Key. Bearer
API keys cannot apply imports.

Accept one database and up to 256 ascending contiguous complete policy-sized
windows, within a 1 MiB request. Each window contains exactly the backend's
advertised meters, nonnegative normalized integer quantities, and the actual
source observation timestamp. Reject open/future evidence and timestamps finer
than PostgreSQL's microsecond precision. Compute costs from the current server
policy; do not accept submitted money, daily totals over hourly windows, or
zero as a substitute for missing meters. Evidence reference and SHA-256 identify
an operator-retained source artifact. The API does not fetch or authenticate it;
the operator must verify its resource mapping, completeness and observation
time before submitting normalized readings. References must contain no secrets
or signed URLs. Raw vendor export parsing and invoice allocation are separate work.

Resolve ownership and immutable backend placement from the catalog. Require a
known provider identity and no active lifecycle lease. Shared restores import
against their independent accounting root, never the descendant. Unknown legacy
tombstones remain unresolved. Confirmed terminal resources accept only windows
through their existing confirmed shutdown boundary; imports cannot establish or
change shutdown, identity, lifecycle state, or an accounting obligation.

Preview returns the previous/imported costs, signed delta, resulting coverage,
preserved observation time, and a revision over the request, actor, policy,
catalog and relevant ledger state. Apply recomputes under the collector's resource
row lock and rejects changed evidence or state. Retain contiguous coverage rules
from creation. Older observations cannot overwrite newer readings; equal-time
conflicting readings are rejected. Window-size changes require a separate migration.
Import time never replaces observation time or proves final provider settlement.

Ledger replacement, coverage advancement and an immutable import receipt commit
in one transaction. The receipt records actor, reason, evidence reference/hash,
request/hash, price policy, preview revision, original/replacement rows and response.
`(account_id, import_id)` durably deduplicates identical requests by the same actor;
conflicting reuse is rejected. Replays return the original response without
reapplying usage. Reapplying the schema migration preserves retained receipts and
reinstalls their audit protection if the migration ledger has drifted.
Evidence is append-only while its account exists, and schema
rollback refuses to discard retained receipts.

Expose `gregale postgres usage-import ACCOUNT_ID --file FILE --json` for preview,
and `--apply --session-file SESSION_FILE` for a reviewed apply,
and typed Go/Node/Python SDK methods. Preview is the default. Applying requires
the operator to put the returned revision in the file's `expected_revision`.
The ordinary CLI login stores a bearer key; it cannot satisfy strict step-up.
The apply command loads an explicit operator session cookie from a private
regular file, without bearer authorization or persistence in the CLI key store.
The receipt's database FK is deferred so the existing account erasure transaction
can remove confirmed tombstones before cascading receipts with the parent account.
Admission continues to evaluate all coverage, freshness, terminal correction and
budget requirements; importing historical windows alone need not unblock it.

## Validation

Memory and PostgreSQL tests cover preview without writes, historical recovery,
cost corrections, durable/concurrent replay, stale previews after collection,
ownership, incomplete/invalid evidence, lifecycle leases, unknown identities,
shared roots, terminal boundaries, cancellation and atomic audit failure rollback.
HTTP/CLI regressions cover authentication, bounded strict decoding, preview/apply
dispatch and safe typed responses. No VM lifecycle code changes.
