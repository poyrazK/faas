# ADR-426: MCP hosting on the application lifecycle

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
promotion checks, durable Tasks backed by Jobs, and customer-isolated
execution. These require separate acceptance and must not be advertised as shipped
by this implementation.

## Local tool contract checks

`mcp lock` captures a deterministic caller-visible tool catalog without execution,
endpoint identity or credentials. Partial discovery cannot publish a snapshot.
`mcp diff` compares local snapshots with conservative input/output directionality;
unknown changed schema keywords and annotations require review. `--check` fails
for both structural breaks and review requirements. References are never fetched.
These commands extend the diagnostic profile without changing deployment ownership
or creating automatic traffic-promotion gates. Compare catalogs captured with the
same permissions; tool behavior and arbitrary schema compatibility remain unproven.

`--strict-catalog` optionally requires review for newly visible tools, including
expansion of an empty catalog. The default comparison keeps additions informational;
strict receipts identify the enabled policy. Per-caller baselines and the reusable
catalog workflow combine nonexecuting discovery with this local gate. The workflow
pins the CLI source, reads the reviewed baseline commit, receives one role's client
token explicitly and retains comparison evidence. This adds a user-controlled CI
check without changing platform promotion ownership. Reader/writer fixtures cover
modern and legacy catalogs, expansion/removal and zero tool execution.

## Application-owned tool authorization

The Node starter accepts an optional `auth.tool_scopes` map alongside endpoint
scopes. A configured map denies unlisted tools, including future registrations;
an explicit empty scope array permits the endpoint's callers. An empty map denies
all tools. Null maps/arrays fail validation, and public mode cannot grant scoped
tools. Omission retains the endpoint-only policy of existing servers.

Verified JWT claims populate request-local SDK auth context. A fresh server
instance disables unauthorized registrations for discovery; actual JSON-RPC calls
are checked before dispatch and tool callbacks have an independent guard. Scope
denials challenge with endpoint plus tool scopes. Caller-supplied headers,
arguments and annotations grant no permissions. Object ownership is checked by
the application inside each callback. The platform does not enforce arbitrary
customer servers' policy merely because they supply this manifest.

Portable acceptance covers both modern and stateless legacy discovery/execution,
alternating/concurrent callers, guessed tool names, forged headers, missing
identity, all-required scopes, empty policies, and redacted logs. This extends the
resource-server contract without a new gateway or deployment policy owner.

## Application-owned execution diagnostics

Versioned request summaries and callback events share an application-generated
UUID returned in `X-MCP-Request-ID`. Capture denials before dispatch, public
Standard Schema input/output validation, callback outcomes and disconnects.
Only registered tool names and closed method/protocol/outcome/reason fields enter
events; never retain caller IDs, JWT claims, arguments, results or error text.
Logging failures cannot change execution or authorization.

`mcp events` reads the existing control-plane application log stream with operator
credentials, validates the event DTO, discards unknown fields and supports local
tool/outcome/request filters. Retention gaps and unavailable streams are explicit
partial results with nonzero exit status. No new telemetry store, gateway policy
owner or VM lifecycle path is introduced. These events are lossy application
diagnostics, not a platform-authenticated audit record or fleet metrics. Existing
servers must update their code to adopt the event contract.
