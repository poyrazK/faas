# ADR-20261105133521: Hosted agent-key catalog selection

Date: 2026-11-05
Status: Accepted
Related: ADR-20261026152009, ADR-20261029085359, ADR-20261026195952

## Context

The hosted bridge used its `--tools` value both as an operator ceiling and as
the catalog of every headless agent key. Its default was `core`, so an agent
could have appropriate native scopes but could not request the operations,
diagnostics, release or provisioning catalog. OAuth grants could select a
catalog, but only within the same default ceiling. Registering a separate
management instance per selection would create competing in-memory copies of
the same key's plans and encrypted journal.

## Decision

The hosted daemon's default operator ceiling permits all toolsets, while OAuth
connections continue to default to `core` and explicitly select their catalog
at consent. The operator may set `--tools` to a narrower maximum. Existing
explicit limits still apply; the catalog never grants API scopes, app access,
environment access or write consent.

On the hosted bridge, agent-key clients may send `Gregale-MCP-Toolsets` with a
comma-separated selection on every MCP request. Omitting the header selects
the focused default. Invalid, repeated and unavailable selections are rejected.
The bridge is stateless at the HTTP layer: the header is request-scoped, so a
client must send it consistently. OAuth grant catalogs ignore this header.
Self-hosted integrations keep their existing behavior unless they explicitly
enable agent-key catalog selection.

One management instance, plan journal, admission and lifecycle are shared by
all selections for a given key. The server registers only the operator maximum;
request middleware filters the listed catalog and refuses calls outside the
selected subset. Structured recovery hints and connection metadata reflect the
selection. Native scope and immutable app checks continue independently on
every request. The tool catalog test guards the SDK page-size assumption.

## Validation

Test both protocol versions, simultaneous catalog selections with shared plan
recovery, omitted and malformed headers, operator limits, read-only and app-bound
keys, OAuth grant isolation, recovery hints and catalog budgets. A deployed
journey selects `operate` with an app-bound key, creates a review-only scaling
plan and reads that plan from its default-core connection.
