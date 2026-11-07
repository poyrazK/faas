# ADR-20261103211147: Native agent authorization previews and app costs

Status: accepted

Extends [ADR-20261103202626](20261103202626-mcp-post-write-verification.md).

## Context

Successful management reads do not establish permission to mutate an app.
The MCP preflight could only report write authorization as unknown. App cost
inspection depended on a local CLI and account billing access, preventing
hosted and app-bound agents from using the same read surface.

## Decision

`GET /v1/apps/{slug}/operation-preview` returns a versioned, read-only snapshot
for deploy, restart, scale or env_change. It uses the authenticated native
scope policy and the actual mutation route's current organization role policy.
The preview also reports account standing, route release and known environment
restrictions. The existing authentication middleware enforces MFA. Native app
identity and current read access are checked again before returning evidence.
Previewing a denied operation creates no synthetic mutation-denial audit.
The env batch route explicitly uses ConfigureApp, matching individual env writes.

Authorization, admission and local MCP consent retain separate meanings.
Authorization covers scope, role and deploy email verification. Exact request
validation and quota admission stay unknown; no reservation, deployment intent,
plan, approval or configuration revision is created. Protected environments
continue to require their native reviewed workflow. Every mutation rechecks
current policy and admission. MCP validates preview version, immutable target,
selected environment, operation, freshness and consistent aggregate statuses.
Old or incompatible servers leave permission unknown rather than inferring it
from successful reads. The existing release tool group owns preflight; core
catalog discovery remains unchanged.

`GET /v1/apps/{slug}/costs` requires usage:read and current app read access. It
reuses the canonical retained financial reader and stable-app-ID allocation
projection for the app's resource payer. It preserves exact integer quantities
and millicents. App results omit account totals, invoices, forecasts, accountwide
unpriced/nonbillable quantities and unrelated allocations. Sampling-window
coverage remains explicit; per-app quantity coverage is unknown. The response
is not a bill estimate. The canonical billing capability gate also controls
this route. Query parameters are strict and responses are private, no-store.

`gregale_get_costs` uses this native endpoint directly, with no CLI or account
billing fallback. It validates returned app and allocation identities. App-agent
credentials may explicitly opt into usage:read; defaults and ordinary deploy
tokens retain their existing scopes. App binding continues to deny global
billing and other apps. A validated expand migration permits the new scope in
the database constraint without rewriting existing tokens. Adding, validating
and replacing the constraint use separate transactions so validation does not
retain the ADD's write-blocking lock. Roll out the schema
before issuing new usage credentials, and update control-plane validators
before relying on them across a mixed fleet. Older validators fail closed.
Rollback refuses while usage-scoped token rows remain; revoke and remove them
before reverting the constraint.

Native OpenAPI, the Go client and generated public/preview Node and Python SDKs
publish both read contracts. MCP schemas require their stable output identities.

## Validation

Native HTTP and protocol tests cover scope and current organization role
checks, separation from consent and admission, app isolation, revocation,
read-only behavior, exact monetary integers and withholding of account data.
The PostgreSQL credential rotation test explicitly requests usage:read.
The deployed app-agent journey adds native preview and app-cost inspection,
including continued rejection of account billing access. Running that journey
requires a configured deployed target and credentials.

Request-specific quota simulation, cost projections and additional post-write
verifiers remain separate work.
