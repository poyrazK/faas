# ADR-793: User-scoped idempotency for the note creation RPC

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Add an optional `create_note_with_tags_once` function and
  application-owned receipts through a new starter migration. Keep the original
  RPC and its signature available for existing callers.
- **Why:** A caller can lose the response after an atomic mutation commits.
  Transaction atomicity alone cannot prevent duplicate operations on retry.
- **Boundary:** Receipts remain in the customer's `api` schema, consistent with
  the existing data_api credential contract. They add no control-plane state,
  resource owner, credential capability, task or quota. Default table grants
  apply. RLS requires both the verified JWT subject and a transaction-local
  scope set only by the owner-authored RPC in the public HTTP surface. Ordinary
  REST requests cannot read, insert, update or delete receipt rows. Schema
  metadata remains visible; the scope is not a boundary against a SQL session
  or another explicitly exposed function that sets the same parameter.
- **Semantics:** A UUID and authenticated subject identify one logical operation.
  Transaction-scoped advisory locks serialize matching requests. The primary
  key, rather than the hash, determines identity. Store the exact typed request
  and original note response in the transaction that writes notes and tags.
  Equal requests replay that response; changed payloads raise PT409. Failed
  operations leave no receipt. PostgREST uses the function's read-committed
  setting, which permits a waiting invocation to observe the first commit.
  Direct SQL callers must use read committed. Query timeout bounds lock waits.
- **Lifecycle:** Receipts have no automatic expiry. The owner must define
  retention, include stored snapshots in deletion/privacy policy and plan
  compatibility with future row-schema changes. Purging a receipt ends its
  duplicate-prevention window. Replaying a retained receipt after note deletion
  returns the historical snapshot without recreating the note. Existing fresh-
  restart and type-export requirements apply after the migration and grants.
- **Validation:** Real PostgreSQL/PostgREST tests cover concurrent equal and
  conflicting requests, separate subjects using the same UUID, constraint and
  RLS rollback, response loss after commit, snapshot replay and direct-access
  denial. Browser, generated-type and clean-install checks remain required.
  Native/provider and live staging qualification remain pending.
