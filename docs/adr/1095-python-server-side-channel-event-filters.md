# ADR 1095: Python server-side channel event filters

Status: Accepted; runtime validation deferred.

## Context

Managed v2 supports exact-match metadata filters and ordered checkpoint frames for
nonmatching retained events. Python clients currently treat checkpoints as unknown
frames and do not bind changed selections to cursor identities.

## Decision

Export generator-preserved immutable RealtimeEventFilter. Validate against existing
metadata limits, copy inputs and expose read-only values plus a canonical SHA-256
configuration fingerprint. Add optional event_filter to dedicated transport and
each multiplexed channel registration. Shared registrations choose their own filter;
inboxes remain unfiltered. Send copied filter values in subscribe and verify
matching message metadata before delivery. Presence/activity is unaffected.

Represent skipped positions with additive RealtimeRecoveryEvent.checkpoint=False
by default. Native checkpoint frames require active nonempty filters and exactly
next received sequence. Keep skips and messages in the same bounded ordered deque;
coalesce consecutive queued skips without crossing message boundaries. Split a
coalesced skip at replay's frozen target and retain its live suffix. Coordinator
advances checkpoint progress without invoking application processing; native
iterators ACK only when advanced after earlier effects. Durable skips follow the
same persisted ACK confirmation/deadline path as matching events.

Extend RealtimeRecoveryTarget with optional filter_fingerprint and coordinator /
shared registration with cursor_filter_fingerprint. Restored nonzero filtered
local progress requires a matching fingerprint. Fresh zero progress adopts the
native target's identity, retained across reconnects. Explicit mismatches fail;
legacy unfiltered cursor defaults remain unchanged. Expose fingerprint alongside
completed progress for caller-owned transactional persistence.

Bind nonempty filtered durable logical names to canonical effective wire names:
pyfilter. plus SHA-256 of logical name and filter fingerprint. A changed selection
therefore uses a separate server checkpoint. Empty filters preserve original names.
Expose effective name on shared handles and derivation on the filter. No automatic
server-state deletion or filter changes in a live registration. Exclude filter
values/fingerprints from diagnostics. These are SDK-side guards, not server-enforced
filter binding; other clients can manually use a different selection on an identity.

## Consequences

Matching consumers replay and progress through skipped positions without callback
side effects or false sequence gaps; sparse streams can complete readiness. Changed
filters require a new cursor/identity and can consume another durable checkpoint.
Fingerprints identify public configuration, not authorization or secrets. Saved
application effects/cursor/fingerprint remain caller-owned; exactly-once is not
implied. External preexisting filtered subscriptions with unsuffixed names remain
separate from derived Python identities. No routes, grants, schema or migrations
change. Static lint, compilation, SDK AST, metadata/export and generator preservation
checks run; runtime tests remain deferred at the user's request.
