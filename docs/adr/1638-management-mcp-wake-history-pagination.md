# ADR-1638: Window-first wake-history pagination

## Status

Accepted. Extends ADR-1634 history discovery and ADR-1637 stage pagination.
Legacy native requests and dashboard aggregation remain unchanged. No migration,
instance lifecycle, event write, plan or permission change. The MCP catalog
remains 44 read/planning tools, 52 with writes enabled and six prompts.

## Decision

Add opt-in pagination=keyset_v1, limit and cursor to the existing app history
endpoint. Filter app and inclusive original since/until in SQL before limiting,
then seek in descending (started_at, instance UUID) order and over-read one.
Normalize bounds and memory timestamps to PostgreSQL microseconds. Exclude
unknown start times; do not infer them. Defaults resolve once to 24h/now;
explicit until must be at or before now, and since must precede until.

Use an optional state read capability implemented by PgStore/sqlc and MemStore,
without extending Store. Join canonical boot metadata (schedd first, then
earliest timestamp/event ID) and earliest completion for each selected wake,
scoping both to the same app. Instance rows remain distinct even when wake IDs
repeat. Metadata is a wake-level observation, not exact instance attribution.
Propagate database errors; never present them as successful empty/partial data.
Malformed metadata also fails closed. Missing metadata remains unknown.

Return optional page metadata with mode, normalized original bounds, limit,
echoed/next cursor, complete, source_status read_succeeded and count_scope
returned_page. Counts, histograms and capacity percentages describe only this
page. Positive readiness is the whole-millisecond boot/completion delta; scalar
zero-presence and capacity-denominator limitations from ADR-1634 still apply.
Source success is evidence of this read succeeding, not capture completeness,
freshness, a unique-wake count or an atomic snapshot. Late/backdated inserts,
retention and state/metadata changes can affect independent pages.

Bound the native cursor to 2048 bytes, native page size default/max to 20/100,
and the MCP envelope to 8192 bytes in limits.go. Native wh1 cursors bind target
account, immutable app ID, original normalized since/until, page size and last
timestamp/instance UUID. They are selection data, not authorization. Keep the
Hobby+ gate and ownership/read-surface authentication on every read. Do not add
instance IDs as public row fields. No migration/index is necessary for semantics;
this decision makes no query-latency qualification claim.

MCP keeps its 20/50 page bounds and explicitly requests keyset support. Validate
source, page count scope, echoed selection/cursor, terminal metadata, microsecond
row timestamps and descending progression. Return next_arguments with a hp1
envelope pinning app slug/ID, original bounds/size and the unchanged native
cursor. Recheck diagnostic consent and immutable app identity on each read.
Never auto-page. Native window verification does not prove retention coverage.

Older APIs may ignore the new parameters and omit page metadata. Initial reads
then retain the ADR-1634 latest-100-before-filter, whole-second, best-effort
compatibility projection with no continuation. A resumed downgrade returns
native_upgrade_required, without repeating or broadening a legacy query.
Existing Go SDK calls remain unchanged; add typed page options and regenerate
Node/Python clients and embedded OpenAPI. No raw metadata, infrastructure IDs,
additional log/environment/write permissions, probes or model calls are added.

## Consequences

Agents can discover older retained instance observations outside the latest 100
candidates and explicitly follow their safe wake IDs to stage evidence.
PostgreSQL/MemStore, native HTTP, SDK and MCP tests cover historical windows,
tied timestamps, app/canonical metadata scoping, source errors, cursor context,
identity replacement and terminal pages. Pinned real Codex checks continuations
across restart/OAuth refresh. Portable fixture/database checks do not qualify
native/provider deployments.
