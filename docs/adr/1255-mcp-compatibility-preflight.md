# ADR-1255: Shared MCP compatibility preflight

## Context

Release qualification already probes exact running deployment replicas through an
allowlisted API. Developers need equivalent checks before source upload, with
stable diagnostics suitable for automation.

## Decision

A shared mcphosting validator performs initialization, stateless-session checks,
advertised catalog discovery, schema validation and optional strict baseline
comparison. Release qualification uses the same validator and retains its
transport checks and explicit authorization policy checks.

`gregale mcp validate` validates a local/public endpoint, or running replicas through
the existing exact-instance protocol API. The latter remains account scoped and
uses the existing RPC allowlist; no arbitrary-URL fetch is added to apid.

An optional `mcp deploy --validate-url` gate checks a separately running endpoint
before account mutations or source upload. This is developer preflight evidence;
it does not bind the running local process to the uploaded source. Existing
candidate readiness, protocol qualification and promotion gates remain required.

Diagnostics omit remote error bodies. Tools are never executed, resource contents
are never read and prompts are never rendered automatically. These checks are
explicitly marked skipped. Authorization rejection requires existing release-policy
expectations. A passed report covers the selected Gregale stateless profile and
protocol version, not universal MCP compatibility or live provider acceptance.

## Consequences

CLI preflight and API-backed release qualification share catalog semantics.
Failures return exit status 1 and stop optional preflight deployments before upload.
This change does not touch VM lifecycle or add a network-fetching server endpoint.
