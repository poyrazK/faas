# Bulk document edits

Run `gregale data-api dev --port 0` here to generate the database contract and
verify document versioning and identity isolation. Install `@gregale/data` in
your application and use the typed `bulkEditDocuments` workflow in `client.ts`.

`bulkMutations(items, build, options)` creates one fresh, selected query per item.
`batchSize` controls scheduling windows (default 100 items), and `concurrency`
bounds active requests (default 2). Every request has its own deadline (default
5 seconds). Results appear in input order, even if requests finish out of order.
Use distinct primary keys for competing edits; processing order within a window
is not guaranteed. The input array is copied, but input objects are not frozen.

Each item is a separate PostgREST transaction. A scheduling window and the
whole run are **not atomic**. The helper does not combine items into an array
insert or a transaction. For an atomic array insert, send one ordinary
`insert([...]).select()` request; any validation failure rolls back that request.
For an atomic multi-step business operation, use an approved SQL RPC.

Per-item outcomes:

- `applied`: selected committed rows are returned with their generated types.
- `conflict`: a conditional edit/delete matched no visible row. Stale versions,
  missing records and RLS-hidden rows share this outcome.
- `error`: a non-5xx server error, preserving the original status and code.
- `uncertain`: a deadline, cancellation, transport/5xx failure or unusable response
  after execution began. Reconcile before retrying; it may have committed.
- `skipped`: cancellation prevented the query factory from being called.
- `invalid`: the synchronous query factory threw before returning a query.

Use `signal` to cancel. Requests already started receive the signal; queued items
are skipped. No failed mutation is automatically replayed. Reuse idempotency
keys for retryable RPCs or use primary-key/version predicates for conditional
writes. Use `select()` with array results and a synchronous factory that only
builds a lazy query. Callback side effects are outside the helper's guarantees.

`onProgress` receives immutable aggregate counts after each completed item,
including skipped items. It does not receive row data. Observer exceptions and
rejected promises cannot interrupt writes; asynchronous observers are not awaited.
Raw errors remain in the result report for the caller, so redact them before
logging or exporting it. Results are retained in memory, making this helper best
suited to bounded application imports rather than unbounded streams.
