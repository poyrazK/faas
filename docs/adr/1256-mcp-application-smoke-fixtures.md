# ADR-1256: MCP application smoke fixtures

## Context

Compatibility preflight validates protocol and catalog definitions without
executing application handlers. Developers need reviewed application assertions
that can block candidate promotion.

## Decision

A versioned project-owned JSON suite defines tool results, resource MIME types,
prompt message roles and missing/malformed bearer rejection. Strict parsing
rejects unknown fields, duplicate case names, empty suites and invalid case shapes.
Diagnostic bounds are one MiB and sixty-four cases.

`gregale mcp test` runs a suite against an explicit endpoint. Any tool case requires
`--allow-tool-execution`, checked before network requests. Calls execute once,
without retries, Tasks or interactive input handling. The caller bounds the suite
with an overall deadline. Reports contain case names and generic diagnostics,
never fixture arguments, response contents, credentials or remote error bodies.

`mcp deploy --fixtures` loads the suite before deployment and runs it against the
zero-production-traffic candidate preview after existing qualification and access
checks, immediately before conditional traffic promotion. A failure leaves the
candidate unpromoted. Credentials come from the existing token environment flag,
not fixture contents. No execution RPC is added to the exact-instance probe API.

## Consequences

Application behavior can become an optional release gate. Preview tests exercise
requests routed to the candidate revision but do not establish coverage of every
replica. Fixtures may affect real downstream systems; use dedicated test data.
Rerunning the CLI or deployment explicitly reruns fixtures and can repeat effects.
Authorization cases verify missing/malformed token rejection, not provider login
or insufficient-scope token validity. Suites are bounded diagnostic evidence,
not exhaustive application tests. No VM lifecycle change applies.
