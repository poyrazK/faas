# ADR-423: MCP hosting on the application lifecycle

Status: Proposed (implementation and qualification in progress)

Date: 2026-10-01

## Problem

An MCP server can run as an HTTP application today, but customers must discover
the streaming, authentication, origin, and restore contracts themselves. A
successful HTTP health check does not establish that MCP tool discovery works.
Dependency-integrity hashes also trigger the source secret scanner, preventing
reproducible Node installs.

## Decision

Provide an explicit stateless Streamable HTTP profile, a Node starter, and
`gregale mcp init|deploy|doctor|tools|call|config`. Deploy delegates to the existing
application control plane and verifies the MCP endpoint after readiness.
`gregale-mcp.json` ships alongside `gregale.yaml`; it describes the application
protocol, not guest resources, and remains compatible with existing apid hosts.
No new scheduler, build path, VM lifecycle, or protocol proxy is introduced.

Support the 2026-07-28 request metadata contract and opt-in stateless compatibility
with 2025-11-25. Stateful sessions and old HTTP+SSE/stdio are outside this profile.
The diagnostic client is bounded and handles JSON and request-scoped SSE. It never
uses the operator's Gregale account token against a customer MCP endpoint.

Authentication must be explicitly public or use an external OAuth provider. The
starter implements protected-resource metadata, bearer challenge, issuer and
audience validation, expiry, an asymmetric algorithm allowlist, and scopes.
The provider owns authorization-server discovery, consent, PKCE, and token issuance.
The platform's public bearer gate is opened explicitly for this profile so that
the application's OAuth resource-server contract receives client requests.

Use exact allowed origins and reject unconfigured browser origins. Log tool name,
duration, and outcome; exclude arguments, results, tokens, and customer identity.
Tool calls are never retried automatically: external side effects may have occurred.
If post-deployment verification fails, put the app in maintenance and require
explicit review before resuming public traffic. A probe does not roll back code.
Doctor discovers tools without invoking them; an explicit call or stream probe
authorizes execution. Preserve lockfiles and suppress entropy only for valid
dependency-integrity digests while retaining provider credential detection.

## Qualification and future work

Portable acceptance covers scaffold, config, JSON/SSE, legacy initialization,
errors, pagination, origin rejection, OAuth token validation, and redaction.
A CLI journey must deploy, discover, call, stream, park, and rediscover on an
existing native host. This changes no VM lifecycle code. Product maturity remains
preview until independent MCP clients and provider integration are qualified.

[The 2026-10-01 receipts](../ops/evidence/20261001-mcp-preview/README.md) record
that journey and the production scanner upgrade still required for an untouched
dependency-lockfile deployment.

Follow-on work includes gateway-owned OAuth and per-tool policy/metrics, contract
diffs and rollout checks, durable Tasks backed by Jobs, and customer-isolated
execution. These require separate acceptance and must not be advertised as shipped
by this implementation.
