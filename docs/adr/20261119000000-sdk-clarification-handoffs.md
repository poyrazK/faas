# ADR-20261119000000 Scoped clarification handoffs

The application can assign the exact pending input gate of an owned pipeline
Task to a named OAuth subject. A durable grant binds the gate, run, Task,
issuer, reviewer resource and subject, and expiry. The reviewer uses separate
MCP read and reply tools that expose the one scoped form. The Task owner
continues to observe the original Task.

Both runtimes apply current OAuth scopes and the existing gate authorizer.
The gate decision statement locks the live grant and records the answer in
the existing gate row. Revocation uses the same grant row lock. The same
reviewer can retry the exact answer while the grant is active, and the
existing gate audit and idempotent wake event capture the decision.
