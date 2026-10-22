# ADR 1084: Python subscription readiness

Status: Accepted; runtime validation deferred.

## Context

An authenticated open transport may have pending, denied or gap-blocked subscriptions. Connection health alone can conceal partial channel readiness.

## Decision

Export generator-preserved immutable subscription status/summary and a tracker. Atomically begin a bounded full channel/inbox requirement set (1024 default, 1–8192), with increasing local callback generations. Reject duplicate/overflow sets before replacement. Ignore older connection attempts and stale/duplicate callbacks. Explicit subscription acknowledgements confirm pending entries; explicit authorization denial and resync-required remain terminal until a new begin generation.

Optional acknowledgement_timeout_seconds (None default; finite 0.1–86400) flags prolonged pending acknowledgements when snapshot is evaluated. Do not convert timeout to denial or send retry. Reset invalidates callbacks, forgets requirements and leaves readiness false; explicitly begun empty sets are ready. Dispose rejects new begins and ignores late callbacks. Isolate synchronous monitoring errors.

Extend health with subscription summary/reasons: explicit denial produces Subscription failed, gap produces Resync required and pending/older-attempt requirements produce Updating. Connection state retains highest priority. Validate input summaries and duplicate identities. Subscription readiness remains independent from replay processing and presence completion.

## Consequences

Transport lifecycle must establish actual required subscriptions and classify server errors correctly. No timer, connection, authorization, subscribe or retry implementation is added. No routes, grants, schema or migration change. Static lint/compilation, SDK AST/export/preservation checks and diff checks verify implementation; runtime validation remains deferred by user request.
