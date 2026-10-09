# ADR-713: Customer Operation HTTP transactions

- **Status:** accepted for internal implementation; production admission stays disabled
- **Date:** 2026-10-07
- **Decision:** An immutable HTTP Operation definition may opt into
  `http_transaction_version: 1`. A validated customer execution advertises
  `X-Gregale-Customer-Operation-Transaction-Version: 1` and the pinned result
  byte limit. The Go, Node and Python SDKs commit business writes and the
  complete JSON business result together in an explicitly installed,
  application-owned PostgreSQL receipt table. A later authorized execution
  returns those exact saved bytes.
- **Why:** A handler can commit business writes and lose its response before
  Gregale records completion. Application retries need a durable result without
  repeating committed writes.
- **Consequences:** Customer Operations use
  `public.gregale_customer_operation_inbox` and the
  `gregale.customer-operation-inbox.v1:` advisory lock namespace. They retain
  their own identity; no managed exclusive operation is created. The SDK
  helpers share ADR-586's transaction engine while using this separate receipt
  table and lock namespace.
  The full response remains the typed business result and passes the existing
  output schema and fenced completion checks. Version zero preserves ordinary
  HTTP behavior. Unsupported versions fail definition compilation.

The receipt key is the customer Operation UUID. Account, app, customer tenant,
and a SHA-256 digest of exact method, target, and body must match before a saved
result is returned. The digest uses `gregale-customer-operation-request-v1\n`
framing. Invocation ID, attempt, capability, and deployment are excluded from
receipt identity so an authorized recovery can retrieve committed work. The
header factory requires a current execution context, negotiated version, and
nonzero owner UUIDs; it is only for requests delivered through Gregale's trusted
guest listener, not authentication for arbitrary HTTP servers. Business
authorization belongs in the handler before transaction or replay.

A dedicated READ COMMITTED transaction acquires the customer receipt lock, then
reads the receipt in a subsequent statement. The callback uses only that
transaction and returns a JSON result. Validation or callback failure rolls back
both business writes and receipt. Commit acknowledgement failure is explicitly
unknown and never automatically retries the callback. A retry checks the receipt
first. Retain receipts while the original Operation can replay; installation and
cleanup are explicit. Named effects are outside this result-only protocol.

PostgreSQL commit and Gregale completion remain separate transactions. A saved
receipt does not renew an execution lease or authorize progress/completion;
stale attempts and inactive customer ownership retain the existing fences.
Callbacks must not control the transaction or make external side effects. A new
Operation UUID represents new work and business constraints remain necessary.
The adapter does not change recovery policy or qualify production/VM readiness.

The authenticated gateway transport disables HTML escaping for customer
Operation results and allows the existing 1 MiB result ceiling plus bounded
JSON envelope overhead. This preserves valid result bytes at the plan limit;
customer metadata never selects a managed result decoder.

Portable acceptance covers rollback, concurrent duplicates, owner/input
conflicts, managed namespace isolation, unknown COMMIT, and HTTP process death
before commit and after commit before reply. MemStore and PostgreSQL acceptance
cover negotiation, authorized recovery, stale dispatch/completion, and retained
typed results.

Source YAML/TOML declarations, CLI validation, immutable deployment bundles,
and build retries preserve the version. Definition listings expose it in the API
and Node, Go, and Python SDKs. The application-owned order example packages the
internal SDK with deployable source. Portable acceptance runs that actual server
through ingress and the scheduler, kills it after commit before reply, and uses
account-authorized recovery to verify one logical Operation, two executions,
one receipt, one business transition, and a retained customer-owned result.
