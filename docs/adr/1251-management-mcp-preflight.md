# ADR-1251: Read-only management MCP preflight

## Status

Accepted. Extends ADR-1250.

## Decision

Expose `gregale_preflight` for an explicitly selected app/environment. Verify
immutable consent before and after inspection, and probe release review, scoped
health and scoped operational summary through existing customer API reads.
Report each check independently. Unavailable or incompatible reads remain unknown;
never infer support for mutations from successful reads or endpoint discovery.

Separate local connection/environment write consent from API admission authority.
Source access, native qualification and exact browser approval remain unknown until
their existing workflows establish them. No source-review POST, plan creation,
approval, active health probe or customer-intent write occurs during preflight.

Report only whether the configured encrypted single-writer store is open and free
of recorded failures. Ephemeral connections are identified explicitly. Never expose
store paths, keys, records or other grants; never test durability by writing.

## Consequences

Agents can identify missing read compatibility and local write consent before
planning. This is a sequential diagnostic snapshot, not a release authorization or
an atomic readiness verdict. Native admission still rechecks credentials, billing,
entitlements, protection and current state. Store status cannot prove future disk
writes or recovery after restart. Upstream probe failures use fixed reasons to avoid
reflecting private response bodies.
