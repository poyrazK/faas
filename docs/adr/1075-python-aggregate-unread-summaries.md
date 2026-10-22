# ADR 1075: Python aggregate unread summaries

Status: Accepted; runtime validation deferred.

## Context

Python consumers track confirmed read receipts but repeat navigation badge aggregation and can accidentally include other readers or treat unavailable streams as zero.

## Decision

Export immutable RealtimeUnreadSummary and summarize_realtime_unread from the existing read-progress module and generator barrel. Require one hashed reader ID and one endpoint's receipt snapshot. Optionally select channels and include the principal inbox (default true). Sum selected confirmed per-stream unread counts, expose sorted receipts, unread-stream count and retained-history gap receipts. Do not estimate missing state or advance read progress.

Validate all receipts with existing bounds and reject duplicate stream/reader identities. Bound receipts and selected channels to 8192, reject duplicate channel selections and require strict boolean inbox selection. Use exact Python integer sums without wrapping. Filter other readers before contributing counts.

Report missing requested channels and inbox separately. Without explicit channels, complete is None because full channel coverage is unknown. With an explicit selection, complete indicates requested receipt presence only; retention gaps and freshness remain separate. Missing entries contribute no fabricated values. Keep summaries immutable.

## Consequences

Navigation badges can distinguish known zero counts from incomplete data. A logical message projected into multiple streams counts in each stream; this is not global deduplication. Account/endpoint isolation and receipt freshness remain caller-owned. No requests, timers, grants, schema or migration changes. Static lint/compilation, SDK AST/export checks and diff checks verify implementation; runtime validation remains deferred by user request.
