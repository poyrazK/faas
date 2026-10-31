# ADR: Data API engine TLS roots and SQL sessions across snapshots

Date: 2026-10-26
Status: Accepted
Related: [ADR-650](650-schema-generated-data-api.md), [ADR-005](../faas_implementation_spec.md) (snapshots are cache), [ADR 20261021180534](20261021180534-data-api-engine-pool-and-server-key-limits.md)

## Context

A trace of the hosted Data API through the code found two faults that no test
covered, because every acceptance run uses a database on localhost.

1. **The engine cannot reach a remote database.** The runtime rewrites every
   non-local `DATABASE_URL` to `sslmode=verify-full` and gives the same URL to
   PostgREST. PostgREST connects through libpq, which has no built-in trust
   store: without `sslrootcert` it refuses with `root certificate file
   "~/.postgresql/root.crt" does not exist`. Node's own client (schema
   inspection) verifies with Node's bundled roots, so the runtime starts, but
   the engine never connects, `/healthz` stays 503 and the first deploy never
   becomes ready.
2. **Snapshots capture live SQL sessions.** The first snapshot is taken right
   after the first readiness, when PostgREST holds its pool session, and park
   reuses that init snapshot for every later wake. A restored guest serves
   immediately. Its captured sessions are dead (new network namespace, the
   database closed them long ago), so the first request after a wake fails:
   with a cleanly ended backend PostgREST 14.3 answers `503` (`57P01`) and
   recovers on the next request; a silently dropped connection can instead
   hang until the request deadline. The platform's own PostgreSQL starter
   already drains its pool in `before_checkpoint` and replaces it in
   `after_restore` for this reason.

## Decision

1. **The engine trusts what Node trusts.** For a `verify-full` URL without
   `sslrootcert`, the runtime writes Node's bundled root store plus
   `NODE_EXTRA_CA_CERTS` to a private file (mode 0600, one per trust bundle
   per process) and adds `sslrootcert=<file>` to `PGRST_DB_URI` only. Node's
   clients keep the default store; an owner-supplied `sslrootcert` is kept;
   local URLs are unchanged.
2. **No snapshot holds an engine session.** The runtime serves two lifecycle
   routes, accepted only as `POST` with guest-init's `X-Faas-Before-Checkpoint`
   or `X-Faas-After-Restore` header (the gateway strips inbound `x-faas-*`):
   - `/__gregale/lifecycle/before-checkpoint` stops PostgREST (SIGTERM, SIGKILL
     after 1.5 s) and ends the coordinated-refresh reader's session.
   - `/__gregale/lifecycle/after-restore` starts a fresh PostgREST and returns
     at once.
   Requests wait for the engine's readiness within their own deadline, then
   proceed; a deadline that runs out answers as before. An engine stopped while
   its VM keeps running starts again on the next request. An engine that exits
   on its own still ends the server.
3. **The CLI registers the hooks.** `data-api create` (including `--resume` and
   `--migrations`) and `data-api deploy` set both hooks with the 2 s maximum
   timeout alongside the existing egress and edge-auth settings. Apps declaring
   `before_checkpoint` get no warm snapshots, so every capture is terminal.

`after_restore` does not wait for the database: waiting up to 2 s for a cold
remote database would fail the restore and fall back to a cold boot, which is
slower than a wake that reconnects behind the first request.

## Consequences

- Every wake starts PostgREST and loads its schema cache: the first request
  after a wake pays one connection and schema-cache load (hundreds of
  milliseconds against a warm remote database) instead of failing.
- The engine reads the schema on every wake rather than only at boot, as it
  already did after any reconnect. The runtime's RPC allowlist and contract
  fingerprint still come from boot, so `data-api refresh` remains how a schema
  change is published; coordinated refresh keeps fencing mismatches in SQL.
- Data API apps created before this change get the hooks with their next
  `data-api create --resume` or `data-api deploy`; until then they behave as
  before. A hook set on an app still running the old image fails its
  checkpoint (404) and the platform falls back as for any failed hook.
- `tests/data-api/remote-tls.test.mjs` runs PostgREST against a database named
  `db.data-api.test` behind a TLS front end with a test CA, and
  `tests/data-api/lifecycle.test.mjs` checks that a checkpoint leaves no
  session in `pg_stat_activity` and that the first request after a restore
  succeeds. A real guest restore on metal is still unexercised.
