# Local patches to Coraza

This directory is `github.com/corazawaf/coraza/v3` **v3.8.1**, used through a
`replace` in the root `go.mod` (ADR-831 amendment 23). The first commit that
added it is the unmodified upstream module, apart from gofmt on one test file
and upstream's `CLAUDE.md`/`AGENTS.md` (instructions for the Coraza repo),
which were removed. Everything else is listed here.

## 1. Cache `FindAll` per collection write (`internal/collections`)

CRS runs every request rule over whole collections such as `ARGS`,
`ARGS_NAMES` and `REQUEST_HEADERS`. Upstream rebuilds the full match list,
one struct per value, for every rule, so a request with N fields costs about
rules × N allocations. On the edge WAF's in-path check this was 65% of
allocations and about 30% of CPU.

- `Map` counts its writes (`gen`) and keeps the matches `FindAll` built for the
  current count. Each caller gets its own slice of pointers, because
  `Transaction.GetField` filters that slice in place.
- `NamedCollectionNames` (the `*_NAMES` views) does the same against its
  collection's count.
- `ConcatKeyed` (`ARGS`, `ARGS_NAMES`) caches while every part reports a
  count. `replaceVariable` now copies the matches instead of renaming them in
  place, so a part's cached matches keep their own variable.
- Tests: `internal/collections/findall_cache_test.go`. Upstream's unit tests
  and its CRS regression suite (`testing/coreruleset`) pass.

Upstream: (PR link once opened). Drop this patch and the `replace` once a
Coraza release includes it.

## Upgrading

Replace this directory with the new upstream release, re-apply the patches
above that upstream has not merged, and run
`GOWORK=off go test ./internal/...` here plus `go test ./pkg/edgewaf` at the
repo root.
