# ADR-20261017214848 · Preserve request identity in response caching

- **Status:** accepted
- **Date:** 2026-10-17
- **Decision:** Replace sorted queries with exact query strings and retain path
  escaping and an explicit empty query delimiter in the edge response-cache key.
- **Why:** Applications can distinguish URLs that the old key merged, causing
  one request to receive another request's response.
- **Consequences:** URL variants occupy separate entries. Decoded path matching
  still controls rules and purges. A new key version isolates older entries.
- **Rejected alternatives:** Sorting only distinct parameter names still assumes
  that applications ignore query order; bypassing every encoded path or repeated
  parameter would unnecessarily disable caching for those routes.

## Context

This amends the `normalizedPath` and `sortedQuery` identity in spec §4.1.2.15
and [ADR-122](122-edge-response-cache.md). For example,
`?id=1&id=2` and `?id=2&id=1` produce different first values in ordinary URL
parsers. `/catalog/a%2Fb` and `/catalog/a/b` have the same decoded Go `URL.Path`,
but applications can route their segments differently. Even `/catalog` and
`/catalog?` are distinguishable request targets.

## Decision

Keep the existing app, deployment, rule, method and host/header variant
dimensions. Record `URL.Path`, `URL.RawPath`, `URL.RawQuery` and `URL.ForceQuery`
without sorting or decoding the query. `NormalizedPath` remains the decoded
path used for rule matching and path purges; `RawPath` preserves the optional
escaped representation. HTTP methods remain case-sensitive: only `GET` and
`HEAD` qualify for the cache.

Construct the key in one helper and carry the entire key into stale serving
and background refresh. Use the `v3` key encoding for both local and Redis
lookups. Older entries must never be read through the new identity; they expire
normally, and the existing app/path/tag purges still find them. No migration or
Redis record-version change is required: additional key fields are optional
JSON fields and the Redis lookup digest includes the new encoding version.

The learned-cacheability observer also uses the request target and hostname,
so observations from different URLs or hostnames do not establish repeatability
for one route. It remains an in-process advisory mechanism and stores no bodies.

## Validation

Handler tests exercise query order, parameter aliases, escaped paths, empty
query delimiters and method case; repeated identical requests still hit. Verify
stale serving, refresh, shared-cache round trips and path purges preserve all
variants. Observer tests require independent first observations across URL and
host variants. Run gateway tests with the race detector and scoped lint.
