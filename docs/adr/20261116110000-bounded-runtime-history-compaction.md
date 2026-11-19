# ADR-20261116110000 · Bounded runtime history compaction

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-16
- **Extends:** [observed build history](20261115100001-observed-build-history.md).

Combined v2 history snapshots exceed the existing 16 KiB event-array budget:
complete single-box and control-plane examples reach about 28 KiB. Appending
such an event then evicting oldest events can discard the newest event too.
Keep a useful latest rollout summary without increasing the 16-event, 16 KiB
history budget or the 1 MiB report limit.

Compute semantic signatures from the original full sanitized summary, before
fitting. Keep existing small events unchanged. If a single event is oversized,
losslessly compact its eleven advisory section summaries into `compact_details`
with `encoding=zlib-base64-v1`, exact `decoded_bytes`, and canonical standard
base64 `data`. Core rollout state, source status, generation clock, counts, job
inventory, readiness and process proof stay directly readable. Never encode
source responses, targets, commands, output, tokens or provider errors.

Bound decoded JSON at `ObsPostDeployBuildHistoryDecodedBytesMax`, reusing the
64 KiB runtime-response ceiling. Require one complete zlib stream, exact decoded
length, known object sections, no duplicate keys, no trailing or concatenated
streams, and no duplicate inline/encoded sections. The collection adapter expands
only within this bound, runs the existing independent summary validators, and
re-encodes the closed projected result. It never forwards an opaque input block.
Progress consistency checks use expanded summaries, while saved event bytes stay
compact. Refit after projection if encoding implementations change serialized
sizes; post-collection older-event removals count as envelope omissions and never
as recorder drops. Fit the newest event before evicting older ones. Human output
transparently renders the expanded diagnostics. Old inline
reports remain readable. Older adapters can retain the core event but may report
new optional per-section history unavailable; health qualification is unchanged.

If the compacted event still cannot fit or the decoded ceiling is exceeded,
retain the fixed bounded core and explicitly list `omitted_sections` in the
closed section order. `detail_omitted_events` counts retained events with such
omissions; `truncated` includes these omissions. Event count accounting remains
`total = retained + dropped + envelope_omitted`, without counting detail omissions
as lost events. Missing retained detail is not a missing source observation or
proof of an empty, clear or successful diagnostic. Current full evidence and
sticky first-fault/recovery/outcome progress stay independent. Existing current
and retained complete proofs must still validate; an explicitly omitted historic
section provides no proof for or against its earlier category flags.

Fit each newest event first, then evict older events for the existing count/byte
budgets. Do not add events for compaction, clocks, preview clipping or delta
magnitudes. Preserve caller ownership, exact retention bytes and report-envelope
omission accounting. No API/SDK shape, source query, poll, threshold, execution,
retry policy, authorization, health result or exit behavior changes.

Verify both complete profiles through Go recorder to Python adapter, historical
fault and recovery retention, malformed/bomb/trailing-stream/duplicate payloads,
privacy after projection, explicit omission fallback, old reports, ownership,
clock-only deduplication, rollover and report-envelope fitting.
