# ADR-426: MCP hosting on the application lifecycle

Status: Proposed (implementation and qualification in progress)

The deployment and gateway-policy decisions below are refined by
[ADR-644](644-mcp-verified-promotion-and-resource-policy.md).

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
ADR-638 replaces the initial maintenance-on-failure flow with zero-traffic
candidate verification and guarded promotion, preserving the serving revision.

`mcp call` may opt in to bounded form-mode Multi Round-Trip Requests (SEP-2322)
with `--interactive` or `--input-responses-file`. The default call remains
single-request; the CLI handles at most three input rounds and 16 form requests
per round. Modern calls may also opt into the `io.modelcontextprotocol/tasks`
extension with `--tasks` or `--wait`. The client can inspect/cancel returned task
handles with `mcp task-get` and `mcp task-cancel`; `--wait` polls using the
server's interval, handles bounded form input when explicitly enabled, and sends
a cooperative cancellation request if its timeout expires. `mcp task-wait`
resumes polling a saved task handle and can answer supported form requests.
The starter can optionally produce durable Tasks for task-aware clients when a
PostgreSQL binding and stable encryption key are configured. By default, the
HTTP process also claims work. Operators can run a separate worker app using
Gregale's existing worker lifecycle; both apps must share the database, owner
key, and explicit task namespace. The worker recovers expired leases. Gregale's
worker custom metrics can read aggregate depth through the starter publisher.
An in-process publisher needs a nonzero worker floor; ADR-638 adds an external
read-only observer for worker scale from zero. Tasks remain disabled by default. Custom task handlers can persist
client input requests and resume through `tasks/update`; embedded elicitation,
sampling, and roots requests are limited to capabilities declared by the
original client. The starter has no built-in sampling, roots, URL elicitation,
stateful sessions. Task and task-resource subscriptions use bounded
request-scoped streams.
Doctor discovers tools without invoking them; an explicit call or stream probe
authorizes execution. Preserve lockfiles and suppress entropy only for valid
dependency-integrity digests while retaining provider credential detection.

## Qualification and future work

Portable acceptance covers scaffold, config, JSON/SSE, legacy initialization,
errors, pagination, origin rejection, OAuth token validation, redaction, and the
Tasks wire flow with a memory store. A separate PostgreSQL 16 integration job
verifies the SQL store's DDL, payload encryption, caller/app isolation, expired
lease recovery, retry exhaustion, cancellation, expiry cleanup, concurrent worker
claims, and replacement-runtime recovery after a worker process crashes. A CLI
journey must deploy, discover, call, stream, park, and rediscover on an existing
native host. This changes no VM lifecycle code.
Product maturity remains preview until independent MCP clients and provider
integration are qualified.

[The 2026-10-01 receipts](../ops/evidence/20261001-mcp-preview/README.md) record
that journey and the production scanner upgrade still required for an untouched
dependency-lockfile deployment.

ADR-638 adds verified candidate promotion, gateway OAuth resource policy and
queue operational safeguards. Real provider/client login, native Tasks scaling
qualification and customer-isolated execution remain release work.

## Local tool contract checks

`mcp lock` captures a deterministic caller-visible tool catalog without execution,
endpoint identity or credentials. Partial discovery cannot publish a snapshot.
Snapshots also record advertised extension identifiers, so clients can review
changes such as Tasks support appearing or disappearing.
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

## Application-owned catalog authorization

The Node starter accepts optional `auth.tool_scopes`, `auth.resource_scopes` and
`auth.prompt_scopes` maps alongside endpoint scopes. They scope tools, exact
resource URIs and resource URI templates, and prompt names. A configured map
denies unlisted entries, including future registrations; an explicit empty scope
array permits the endpoint's callers. An empty map denies all entries of that
catalog type. Null maps/arrays fail validation, and public mode cannot grant
scoped entries. Omitting each map retains endpoint-only policy for that catalog
type.

Verified JWT claims populate request-local SDK auth context. A fresh server
instance builds a caller-filtered catalog; actual JSON-RPC operations are checked
before dispatch and callbacks have an independent guard. Scope denials challenge
with endpoint plus catalog-entry scopes. Caller-supplied headers, arguments and
annotations grant no permissions. Object ownership is checked by the application
inside each callback. The platform does not enforce arbitrary customer servers'
policy merely because they supply this manifest.

Portable acceptance covers both modern and stateless legacy discovery and
execution, alternating/concurrent callers, guessed catalog identifiers, forged
headers, missing identity, all-required scopes, empty policies, and redacted tool
logs. This extends the resource-server contract without a new gateway or
deployment policy owner.
