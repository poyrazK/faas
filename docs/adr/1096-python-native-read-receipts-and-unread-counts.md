# ADR 1096: Python native read receipts and unread counts

Status: Accepted; runtime validation deferred.

## Context

Python has confirmed read-progress trackers, freshness, badges and coalesced
publishers, while native channel/inbox transports only consume receipt history
bounds internally for recovery. Applications still need receipt/action adapters.

## Decision

Export generator-preserved RealtimeWebSocketReadProgress as an optional dedicated
transport or per-multiplexed-channel binding. Select an explicit verified server
SHA-256 reader key for unread summaries; do not infer the socket principal from
channel broadcasts, which can describe other readers. Feed strictly validated
receipts into the existing bounded tracker; expose selected summaries/badges and
safe read errors. Enable receipt broadcasts on subscribe. Existing resubscription
read-state queries refresh receipt data without advancing markers.

Expose mark_read and refresh for channels and inboxes. Assert strict int64 read
positions no further than locally completed processing/ACK progress. Socket-write
completion is not a confirmed read receipt; never optimistically adjust counts.
Processing, durable/filter ACKs and replay completion remain independent of user
visibility. Read assertions on filtered streams refer to whole-channel positions.

Retain last confirmed counts marked stale on session/transport failure; fresh
matching receipts renew them. Incoming positions beyond confirmed history bounds
mark the presentation Updating and coalesce read-state queries using one bounded
refresh timer/task per binding, default one-second interval and five-second request
deadline. Query failures stop that refresh; later data or explicit refresh can try
again. Cancel refresh work synchronously on invalidation and join it on session
cleanup. Multiplexed unsubscribe disposes bindings; dedicated callers dispose
once recovery stops. Guard session identity against obsolete invalidations.

Route allowlisted read errors to safe callbacks without stopping retained delivery
once its initial target is known. Initial read-state failures remain terminal or
explicitly retryable because recovery cannot establish its target. Supply unread
summary/details to existing health and redacted diagnostics. No raw principal,
credentials, payloads or response bodies enter diagnostics.

## Consequences

Native channel/inbox clients expose confirmed unread counts and live receipts
without a custom adapter. The caller owns verified reader-key selection and keeps
bindings within one stream/endpoint/principal/loop. Shared refresh/read operations
must fit the server's existing rate limit. Unknown/stale counts are not fresh zero;
filtered delivery does not filter whole-channel unread counts. No routes, grants,
schema or migrations change. Static lint, compilation, SDK AST and generator
export/preservation checks run; runtime tests remain deferred at the user's request.
