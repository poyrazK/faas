# ADR: Signed-in Data API requests require row-level security

Date: 2026-10-26
Status: Accepted
Related: [ADR-650](650-schema-generated-data-api.md), [ADR 20261018170742](20261018170742-data-api-access-modes-and-clients.md) (public reads), [ADR 20261026131131](20261026131131-data-api-hosted-tls-roots-and-snapshot-sessions.md)

## Context

A `data_api` binding's login gets `SELECT, INSERT, UPDATE, DELETE` on every
table in `api` and default privileges for tables created later. Row-level
security is the only per-user boundary, and nothing required it: a table the
owner forgot to protect, a view running as its owner or a materialized view was
readable and writable by every signed-in user. Anonymous public reads already
required RLS tables or security-invoker views; signed-in access did not.

## Decision

1. **Row-secured relations only.** A signed-in request (an application JWT)
   may reach a relation only if PostgreSQL decides its rows: a table with RLS
   enabled, or a view with `security_invoker`. The rule covers the request's
   target and every relation embedded through `select`, including the target of
   an approved computed relationship. Anything else answers `403 rls_required`
   before the engine sees the request.
2. **The boot contract decides.** The runtime reads the exposed relations and
   their protection at boot, with the rest of the serving contract. A relation
   created since then answers `404 not_found` until `data-api refresh`. Select
   syntax the runtime cannot classify is refused only in a schema that has an
   unprotected, unexempted relation.
3. **Owner exemptions.** `DATA_API_ALLOW_WITHOUT_RLS` (`data-api configure
   --allow-without-rls api.countries,...`, removed with
   `--no-allow-without-rls`) lists relations every signed-in user may reach
   without RLS, in the public-read format and bound (64 entries). Local
   development reads `allow_without_rls` from the dev config and enforces the
   same rule.
4. **Unchanged:** server keys (trusted backends) are not checked; anonymous
   requests keep the public-read rules; file storage keeps `storage_objects`'
   own RLS requirement; RPCs run as security invoker, so the tables they read
   apply their own policies.

## Consequences

- A Data API whose tables all use RLS behaves as before. One with an
  intentionally shared table needs an exemption; one with an unprotected table
  stops exposing it, which is the point.
- Security-invoker views are trusted to the RLS of the tables they read, as for
  public reads; a view over an unprotected table is the owner's choice.
- The check is a request filter, not a SQL privilege: a relation exposed to
  PostgREST after boot (an engine reconnect reloads its schema cache) answers
  `404` until a refresh rather than being served unchecked.
- `tests/data-api/row-security.test.mjs` covers RLS tables, security-invoker and
  owner-rights views, materialized views, embeddings, writes and exemptions
  against PostgreSQL and the pinned engine.

## Amendment 1 (2026-11-05): the gate is reread while serving

The gate read the catalog once, at boot, and every wake restored that read
from the snapshot, while PostgREST's schema channel is off. A migration that
disabled RLS on a served table, or dropped and recreated it without RLS, left
the gate allowing every signed-in user until the next `data-api refresh`; a
public relation or public RPC made unsafe stayed open to anonymous callers.

The runtime now rereads the gate: which exposed relations are row-secured,
and which public relations and public RPCs fail the boot checks. It rereads at
most every `DataAPIAccessRecheckInterval` (one minute) while requests arrive,
without holding them, and after every snapshot restore, where requests wait
for it within their deadline like they wait for the engine. A relation that
lost RLS answers `403 rls_required`; an unsafe public relation or function
answers anonymous callers `503 data_api_public_read_unsafe` or
`data_api_public_rpc_unsafe` until the owner fixes it. A failed reread keeps
the last state and is retried by the next request (the engine cannot reach the
database then either) and is logged as `data_api_access_recheck_unavailable`.
Each reread is one short catalog connection, like the boot checks, inside the
control headroom of the connection budget. The window between a migration and
the gate is now at most a minute, not until a refresh.

## Amendment 2 (2026-11-07): live reads apply the gate

Live reads (ADR-20261016151947) compile a Data API read in the database's hub
and run it as the binding login. They had no row-security gate, so a table
without RLS was fully readable to every signed-in user over a live
subscription, while REST answered `403 rls_required`. The hub now resolves
whether each relation a live read reaches is row-secured, using the same rule
as the runtime: a table with RLS on, or a view with `security_invoker`. It
checks the target and every embedding, and refuses with a `forbidden` error
naming `rls_required`:

- a signed-in read reaches only row-secured relations, or ones listed in
  `allow_without_rls`;
- an anonymous read reaches only row-secured relations, as the public-read rule
  requires, even if a relation is exempt;
- server keys are not checked, as in REST.

The runtime adds `allow_without_rls` to the access settings it reports, and
realtimed puts that list on every RLS-mode frame it sends the hub, replacing
whatever a client sent. A hub reached by an older realtimed gets no list, so it
refuses exempt relations: the check fails closed.
