# ADR-1237: Atomic stored MCP access-policy promotion fence

- Status: Accepted
- Date: 2026-10-12
- Related: ADR-961, ADR-962, ADR-963

## Context

CLI access audits precede conditional traffic promotion. A concurrent JWT rule
edit, including an overlapping wildcard owned by another app in the account,
can invalidate the audit while the serving-deployment comparison still passes.

## Decision

Traffic requests may carry `expected_mcp_access_policy` with the reviewed HTTPS
resource and a version-1 SHA-256 fingerprint. The shared fingerprint includes
all account JWT rules potentially selecting endpoint POST/GET or protected
resource metadata GET, including disabled and expired rules. It includes
selector conditions, JWT actions, priority, app/account identity, enablement,
mode and expiration. JSON object order, rule order, method order and descriptive
metadata do not affect the fingerprint. Expiration state is evaluated again at
the transaction's check. Conditional and invalid path selectors conservatively
overlap. Unrelated hosts are outside this condition.

Apid attaches the condition to the authorized app/account. Both stores compare
inside the existing traffic update serialization boundary. MemStore holds its
mutex. PostgreSQL acquires an account advisory transaction lock after existing
app/deployment locks and reads with a new READ COMMITTED statement snapshot.
A deferred edge-rule constraint trigger acquires the same lock before every
INSERT/UPDATE/DELETE commits, including manifests and version rollback. This
covers phantom insertions without locking rule rows or serializing unrelated
accounts. Hash collisions may add contention but cannot weaken the fence.
Account reassignment locks both old and new account keys in deterministic order.
Standard transaction deadlock/timeout handling rolls back rather than bypassing
the condition. No network calls occur while the lock is held.

Mismatch yields `409 mcp_access_policy_changed` before traffic writes or success
notifications. Requests without this optional field retain existing behavior.
The CLI forwards successful audit evidence; task release and restoration use
fresh endpoint verification evidence, never persisted journal evidence. A
policy rejection cannot be mistaken for a lost successful PATCH response.

## Consequences

This condition protects stored customer intent at the promotion transaction.
It does not prevent later edits, certify runtime JWT checks, or wait for gateway
cache convergence. Operators must roll out the migration/control-plane support
before using the new CLI gate. The fingerprint contains no raw claims, headers,
JWT actions or credentials; it is a concurrency condition, not an attestation
or a substitute for authorization of traffic updates.
