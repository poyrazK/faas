# ADR: Python cached acknowledgment recovery-health monitoring

- Status: Accepted
- Date: 2026-10-17

## Context

Node exports retained parked/due/stalled recovery health through a bounded cache
and shared Prometheus/Grafana pack. Python needs the same operational view with
native async connection ownership, microsecond timestamps and joined cancellation,
without querying storage during scrapes or duplicating monitoring definitions.

## Decision

Add frozen/slotted Python metric options, samples and snapshots plus one fixed-
memory owner-scoped cache. Require exact current dataclass types and native
scalar values. Validate owner, coverage, bounded counts and relationships before
copying immutable metadata. Malformed/failed reads clear cached sample data while
retaining the database timestamp high-water mark. Fresh incomplete observations
also advance it. Repeated/regressing/future/stale evidence cannot become eligible
through elapsed time. Internal wall/database instants and monotonic ages use
integer nanoseconds; snapshot ages use finite seconds and timestamp exposition
preserves canonical microseconds exactly.

Reuse Node's thirteen fixed gauge suffixes, HELP text, prefix, content type and
no-label/no-explicit-sample-timestamp contract. Before a valid read or after
failure, omit sample gauges rather than manufacturing zeroes. Snapshotting checks
both wall age and initial age plus monotonic elapsed time; wall-clock regression
also invalidates evidence. Emit only metadata, without protected data, owner or
invocation identity and without arbitrary error causes.

Provide explicit Python metrics polling with one matching owner/poller per cache.
Reuse the existing health watch's joined read/deadline and owned-operation/pause
protocols. Generalize only its read-settings type through a structural protocol;
keep the reader body and ordinary watch behavior unchanged. Copy normalized
health/poll/query settings before async work. Native/event cancellation joins
acquisition, query and cleanup, then clears the cache and returns the existing
cancellation category. No active driver work is raced/abandoned or overlapped.
Applications still bound actual driver calls and serialize other cache access.

Add an explicit same-loop ASGI example: one cache/poller/process, cache-only HTTP
scrapes and no import-time work. Add a distinct Python scrape job on port 9467
while preserving Node's existing 9466 job/config. Reuse all five recordings, six
alerts, panel queries/identities/thresholds and target-label/prefix contracts.
Dashboard version 2 changes only its language description. Extend shared setup
and runbook with Python lease, precise-guard, command-identity and uncertain-
commit recovery mappings. Add static CI config coverage and generator/root export
registration; do not modify existing tests, fixtures or gate assertions.

## Consequences

Shared metric names do not translate protected jobs or typed checkpoints. Each
target represents one retained owner scope; duplicate observers cannot be summed
into a global backlog. Apps own authorized scope mapping, host scheduling,
listener protection, bounded driver/HTTP cleanup and joined shutdown. Scraping
starts no SQL/callback/recovery work and no alert grants additional claims.

Prometheus backlog resolution can follow unavailable evidence and does not prove
recovery. Inspect companion unknown/down/absence alerts and authoritative rows;
use the checkpointed SDK watch for retained incident identity across evidence
loss/restart. A guarded budget grant or supersession can reduce parked count
without confirming the callback. No platform migration/schema/deployment changes.

## Validation

All seven modified/new Python source/generator/example files parse for Python
3.11 and Ruff passed. Isolated generation excluding tests produced 6531
byte-identical Python/SQL/typing files. Sixteen helper/root/typing/SQL sources
match source bytes in both wheel and sdist, with current README metadata, three
generator registrations and seven unique public root exports. Static Node/Python
comparison matched all thirteen gauge suffixes, HELP strings and emission order.
Reader/watch AST is unchanged except its settings annotation; health SQL,
cancellation/recovery, DDL, Node exporter and shared rules/config stay byte-identical.
Dashboard identity/queries/thresholds are preserved. Prometheus 2.54.1 accepted
the Python config and shared rules. Config/CI YAML, metadata, ADR uniqueness,
documentation routes, twenty-nine local links/anchors and whitespace checks passed.
Existing CI changes only add the new config to static lint. No tests/assertions
were added or changed, and no local tests or SDK helper smoke calls ran. Hosted qualification
remains pending for clock changes, stale/incomplete/repeated evidence, query/
cleanup cancellation, same-loop ASGI scraping, alert transitions and rendering.
