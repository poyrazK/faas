# ADR-1677: Ranked, indexed note search under RLS

Status: Accepted

## Context

The Data API starter has filtering, cursor pagination and change feeds, but no
ranked search contract. PostgreSQL full-text predicates are not leakproof and a
GIN text-search index may be unusable under the serving role's RLS barrier.
Search must preserve invoker security and current note visibility.

## Decision

Migration `0016_note_search.sql` adds `search_notes(search_text, after_cursor,
page_size)`, a STABLE, SECURITY INVOKER, approved RPC with a pinned pg_catalog
search path and PUBLIC execution revoked. Normal owner permission setup grants
it to the restricted API login. No extensions or privileged functions are used.

An internal `note_search_terms` projection stores distinct English lexemes by
subject and note id. Its `(subject, term, note_id)` B-tree supports candidate
lookup with equality predicates that remain indexable under RLS. A second
identity index supports maintenance. Invoker statement triggers use transition
tables to batch posting updates in the same transaction as each insert, update
and delete, avoiding a separate scan per row in bulk work; the migration
backfills existing notes. Scope policies hide direct API reads and deny direct
writes. A trigger-depth/scope guard also rejects ordinary owner DML on postings.
The trusted schema owner can still alter schema or disable triggers.

Candidate ids must contain every distinct lexeme from the query. Each candidate
is joined back to current `api.notes` under its own RLS and checked with
`plainto_tsquery('pg_catalog.english', ...)`. This is English stemming and
stop-word removal with AND-word matching, without Boolean, prefix, fuzzy or
phrase syntax. Ranking uses `ts_rank_cd` normalization 32, rounded to integer
millionths, then descending id for ties. Only current note fields and rank are
returned, without snippets, totals or historical values.

Pages return `{results, has_more, next_cursor}`. A size-plus-one query provides
continuation without counting all matches. Opaque cursors bind to the exact
query text and verified subject and carry integer rank/id positions. They are
validated before casts and are not signed or authorization credentials. Reads
continue to enforce current RLS, even with a forged position. This is live
keyset pagination, not a snapshot: edits can move rows between pages. Restart
when the query, identity or API changes. Cursors do not bind to a database epoch.

Central bounds in `pkg/api/limits.go`, checked against SQL and client copies:
query 1,024 UTF-8 bytes, 16 distinct normalized terms, page 100, cursor 128 ASCII
bytes. Default page size is 20. Empty or stop-word-only queries are invalid.
Invalid arguments return PT400 with `note_search_query_invalid`,
`note_search_page_size_invalid` or `note_search_cursor_invalid`. Existing runtime
statement and response budgets apply; common terms can still produce a large
candidate set to rank. Page limits do not bound all search work.

The typed starter exposes `notes.search(text, {after, size})` and a strict,
immutable `parseNoteSearchPage` helper. Requests preserve cancellation and native
errors and disable automatic retries. No UI or background search is added.

## Consequences and validation

Index storage and write work grow with distinct lexemes in note bodies. Body
values must fit PostgreSQL's full-text parser limits. Owner TRUNCATE, disabled
triggers or changes to dictionaries require a reviewed transactional rebuild of
the projection. Ordinary supported writes, rollback and concurrent reads retain
transactional consistency. Existing custom restrictive note policies remain
authoritative; hidden postings cannot broaden note access.

Client tests validate ordering, cursor/page shape, byte bounds and cancellation.
Native acceptance covers backfill, ranking and ties, stemming, RLS isolation and
additional restrictive policies, writes/deletes/rollback, scope restoration,
read-only execution, malformed arguments and a real serving-role candidate
query plan using the term primary-key index with RLS active.
