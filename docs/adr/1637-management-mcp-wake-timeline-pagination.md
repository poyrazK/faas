# ADR-1637: Lossless retained wake-timeline continuation

## Status

Accepted. Extends the bounded timeline and request-to-wake workflow in ADR-1635
and ADR-1636. Supersedes ADR-1635's no-continuation decision only when the native
API supplies validated keyset metadata. Legacy reads remain compatible. No
migration, VM lifecycle, event write or deployment change. Catalog remains
44 read/planning tools, 52 with writes enabled and six prompts.

## Decision

Add opt-in pagination=keyset_v1 and an opaque cursor to the existing native
timeline endpoint. Unchanged legacy requests retain their timestamp hint and
response shape. Keyset responses add optional page metadata: mode, original
normalized since, echoed cursor, next cursor and complete. The existing Go SDK
method stays unchanged; add a typed page-options method and regenerate the
Node/Python clients and embedded OpenAPI copy.

Keep native ownership, Hobby+ plan, MFA/read-surface checks and operator target
resolution on every read. The new optional state capability filters app and
wake before canonical boot selection, then applies exclusive since and a total
(at, event ID) seek before limiting/over-reading one. Implement it in MemStore
and PgStore through sqlc without extending the broad state.Store interface.
Normalize memory timestamps and supplied lower bounds to PostgreSQL microseconds.
Preserve canonical scheduler boot selection before every seek so a corroborating
mirror cannot reappear on a later page. Foreign rows cannot consume page slots
or determine another app's canonical boot marker.

The bounded versioned native cursor carries target account, immutable app ID,
wake ID, original normalized since, limit and the last timestamp/event-ID tuple.
IDs use canonical decimal strings to preserve the full PostgreSQL bigint range.
Reject changed context, malformed/oversized tuples and invalid page bounds.
Cursors are selection data, not credentials; authorization never derives from
them. Native event IDs are not added to public event fields. Keep unfiltered
unknown-wake 404s; filtered or continuation reads can end with an empty page.

Centralize native defaults/maxima 200/1000, native cursor bound 2048 bytes,
timestamp/ID bounds 64/128 bytes and MCP envelope bound 8192 bytes in limits.go.
MCP retains its existing 50/100 event limits and one native read per call.
Request keyset mode, validate echoed mode/selection/cursor and coherent terminal
metadata, and return explicit next_arguments only for verified keyset responses.
Wrap the opaque unchanged native cursor with app slug/ID, wake, original since,
limit and the visible tail timestamp. Bind the next read to that context and
recheck consent/identity before/after each native read. Permit equal visible
timestamps across pages; reject backwards pages or nonprogressing native cursors.

MCP reports pagination_status keyset_at_id_v1, native_since_echo window
verification and page_complete. Complete is the end of this retained live read,
not full capture, freshness or a frozen snapshot. Late/backdated inserts,
retention or changed canonical observations can affect pages. Preserve unknown
source completeness/freshness and page-local stage summaries; never auto-page.

Older native servers can ignore the new mode and omit page metadata. First
reads then keep the bounded timestamp_only_not_lossless projection without
manufactured continuation. A resumed read requires keyset metadata; if support
disappears, return native_upgrade_required rather than repeat a legacy page.
Never translate a timestamp hint into a tuple cursor or advance by epsilon.
No payload, infrastructure identity, additional environment/log/write permission,
probe, replay, collection or mutation is added. Update guidance accordingly.

## Consequences

Agents can explicitly inspect longer retained timelines without losing tied
events at static page boundaries. Existing callers keep their legacy behavior.
Store/PostgreSQL, API ownership/plan/compatibility, SDK, MCP protocol and pinned
real Codex fixtures cover tied times, canonical/foreign rows, cursor context,
page boundaries, identity changes and restart/refresh continuations. Portable
fixture/database validation does not qualify native/provider deployments.
