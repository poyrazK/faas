# ADR 1073: Python confirmed read-progress tracker

Status: Accepted; runtime validation deferred.

## Context

Managed realtime already shares explicit read progress across devices for verified principals. Node tracks read/latest/oldest watermarks; Python applications need equivalent local ordering and unread-badge state.

## Decision

Export immutable RealtimeReadProgress and handwritten RealtimeReadProgressTracker, preserved with generator barrel/module rules. Consume confirmed server receipts only. Key by inbox/channel and stable lowercase SHA-256 reader ID; never broadcast or accept raw identity claims as reader keys. Channel and inbox streams remain separate.

Validate exact nonnegative signed-64-bit integers, strict booleans, sequence <= latest and oldest in 1..latest+1. Ignore exact duplicates and any backward read/latest/oldest bound, matching Node watermark behavior. Accept equal-bound changes in unread/history status because retained edits/deletions affect counts. The protocol does not order equal-bound count changes by revision; callers should refresh authoritative state after potentially reordered delivery.

Bound stream/reader entries to 1024 by default (1–8192). Reject overflow without eviction. Return sorted immutable snapshots and notify a synchronous nonblocking callback on accepted changes/reset/dispose; isolate ordinary callback failures and close accidental coroutines. Reset may target a channel, all inboxes, all channels or all streams. Dispose clears local memory and rejects ingestion. Use one execution context and introduce no timers or transport.

## Consequences

Python consumers can render server unread counts and confirmed seen positions without inventing read markers. Reconnect reset affects local views only; existing authorization, explicit mark-read behavior, server retention gaps and durable state remain unchanged. No routes, grants, schema or migration change. Static lint/compilation, SDK AST/export/preservation checks and diff checks verify implementation; runtime validation remains deferred by user request.
