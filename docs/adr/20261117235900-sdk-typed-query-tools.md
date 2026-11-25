# ADR: Typed query tools and authorized MCP resources

Status: Accepted (SDK preview)

## Context

SDK pipelines and outbound actions support durable work and approval, but agent
hosts still write separate query schemas, cursor handling and resource access
checks to discover the application context needed to choose that work.

## Decision

Add matching Node/Python typed query definitions using existing portable
`JobContract<Input, Row>` schemas. Applications own fixed, read-only handlers and
current authorization; actual database queries must retain ownership/RLS checks.
Authorize before and after each load and refuse a changed subject or owner.
No raw SQL or credential is exposed through tool arguments.

Return bounded pages and lazy iteration with one total deadline, cancellation
and explicit page/item budgets. Sign portable cursor envelopes with an
application-owned HMAC key, binding definition/revision, contract, scope, input
and principal while preserving the backend position as an opaque string.
Pagination remains live, not snapshot isolation; cursors grant no authority.

Expose fixed authorized UTF-8 resource loaders, resource templates, typed MCP
query descriptors, structured results and separately authorized resource links.
The existing host owns protocol negotiation, authentication and transport. Do
not add a second MCP server, subscriptions, remote URL fetcher or mutation path.

This adds no native API, platform schema, plan quota or owner bypass. The example
materializes its private query context with application migration/backend roles
and uses read-only transactions. Existing SDK callers need no migration.

## Consequences and evidence

Applications retain a shared backend cursor key; rotation invalidates cursors.
Handler semantics/order changes require a revision bump. Local cancellation
fences late results but database/request timeouts still bound remote work.
Callbacks are application code and must enforce the advertised read-only policy.

Shared fixtures and installed-package checks cover both languages. Tests exercise
two-user concurrency, changed context/cursor bindings, revocation during/between
pages, output budgets, total deadlines, cancellation and resource access.
The paid CSV journey reads row/failure/payment context before approving recovery;
it uses real PostgreSQL and Chrome with native/Stripe protocol fixtures.
