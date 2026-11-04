# ADR-20261103094121: The operator events list reads events newest first through an index

- **Status:** accepted
- **Date:** 2026-11-03
- **Amends:** ADR-091 §3.7 (operator observability backend: events list)

## Context

`GET /v1/admin/obs/events` (`ListAllEventsPaged`) returns the newest events
that match optional actor, kind-prefix, subject and `since` filters
(`order by at desc, id desc limit N`). `events` has no b-tree on `at`: only
`events_at_brin_idx`, which narrows a time range but cannot return rows in
order. The query's comment expected kind-prefix reads to use
`events_kind_at_idx (kind, at DESC)`, but `kind like $2 || '%'` with a bound
pattern under the database's default collation cannot use it.

So any read without a `since` or a prefix matching a partial index scans and
sorts the whole table. On production (about 750,000 rows) the unfiltered list
and `kind_prefix=operator.action.view` both answer 503 "could not list events"
after about 5 seconds, which breaks the console's "Platform events" tab. The
`LIKE` also treated `_` in a prefix as a wildcard.

## Decision

1. **A b-tree index `events_at_id_idx` on `events (at DESC, id DESC)`**, built
   concurrently. Scanning it returns rows in the list's order, so the query
   filters as it walks and stops after `limit` matches, with or without
   filters, in custom and generic plans alike. No query change is needed for
   this; the previous release benefits as soon as the index exists.
2. **The kind prefix is matched literally** with `starts_with(kind, $2)`,
   as MemStore's `strings.HasPrefix` already did.

The BRIN index stays: other range reads use it.

## Consequences

- The unfiltered list and common prefixes answer in milliseconds.
- A filter that matches only rare or old rows still walks the index back
  until it finds `limit` of them; `since` bounds that walk. If such reads
  become common, the list can default `since` to a recent window.
- `events` keeps one more b-tree, updated on every insert (append-only,
  monotonic keys, so writes stay at the right-hand edge).
