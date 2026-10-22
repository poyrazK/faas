# ADR-1029: Binding preflight against stored release policies

Status: Accepted

## Context

Binding release policies can require storage operations, fresh verification, and
application acknowledgements. A CLI preflight using only explicit flags can pass
while the server's release gate blocks the same deployment. Operators should be
able to inspect those blockers before requesting a traffic change.

## Decision

Add opt-in `bindings check --release-policy`. Resolve and pin the live deployment
and scope, confirm its app identity, then evaluate fresh inventory against the
stored policy. When enforced, use the shorter local/stored verification age,
combine ACK requirements, disallow unsupported waivers, and combine storage
requirements without weakening either side. Different modes for a prefix require
`read_write`; additional local prefixes remain required. An off policy keeps only
local constraints. Checks without the flag retain their existing behavior.

After every inventory evaluation, read and validate the policy again. Its app,
scope, revision and content must remain unchanged. Missing endpoints, read errors,
invalid responses and changed policies fail closed with a structured blocker.
The opt-in check has an overall timeout and supports cancellation. Waiting only
observes scheduled work, including pending storage canaries; missing or terminal
evidence cannot become a pass by waiting.

Return the policy snapshot and confirmation status alongside the existing CLI
JSON report. This is CLI metadata, so the shared inventory and SDK contracts do
not change. No check schedules a task, changes a policy, performs provider I/O,
or writes traffic.

## Consequences

Operators can check the requirements promotion will enforce and see which policy
was observed. Confirmation is an observation at the last read; it is not atomic
authorization for a later traffic change. Server-side catalog and policy fences
still decide at the locked traffic write. Write canaries remain explicit opt-in,
and application ACKs remain independent self-attestations. Read requirements
continue to accept bounded bucket listing rather than object byte reads.
