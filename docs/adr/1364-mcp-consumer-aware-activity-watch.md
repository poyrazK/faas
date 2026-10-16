# ADR-1364: MCP consumer-aware activity watch

Status: Accepted

## Context

Durable named consumers and raw activity watches require callers to copy saved
cursors and carry revision/generation guards between tools. A concurrent
acknowledgment or retirement can make a waiting observation stale. Agents need a
bounded consumer-name-based watch without implicit acknowledgments or baseline
selection.

## Decision

Add read-only `gregale_watch_environment_cache_activity_consumer`, scoped by app,
project, named environment and consumer. Read the current encrypted-journal
record and capture its revision and connection generation. Reuse activity page
and wait bounds (20/50 events, 20/45 seconds), one-second polling, existing short
request admission, read consent, authorization deadlines and observation drain.
No native endpoint, background subscription or journal mutation is added.

Return saved cursor/revision/generation at the top level and the observed event
page separately. For events, include an acknowledgment argument object with the
page cursor and captured guards. Only the caller may submit that object after
processing the page. Empty successful timeouts preserve saved progress and omit
acknowledgment arguments. Request cancellation, earlier parent deadlines and
native failures remain errors; an observation timeout requires a prior validated
empty page.

Check durable grant/storage health, revision and generation before and after
each native poll and before timeout responses. No store mutex spans native I/O
or polling waits. A detected revision change returns checkpoint_conflict;
retirement returns consumer_generation_conflict. Withhold the old page and ACK
draft on either conflict, including races with native errors or expiry. Fresh
read consent and immutable identities continue to be validated by the native
activity reader and tool authorization middleware.

Uninitialized consumers receive immediate recovery guidance after current feed
access validation, without choosing an initial checkpoint. Validated expired
progress yields cursor_expired/recovery_required with unchanged saved progress
and scoped recovery metadata, never an event page or acknowledgment draft.
Rebuild and process a complete baseline before explicitly acknowledging its
cursor; replacing expired progress still requires baseline_rebuilt=true.

## Consequences

The catalog has one additional read tool. Existing raw-cursor watches share the
polling loop and retain their cursor/timeout contract. Lag observations remain
specific to exact consumer gets, avoiding hidden metrics sampling on polls.
Encrypted state and its existing generation fences survive reconnect and bridge
restart. Unacknowledged pages replay, concurrent watchers may see duplicates,
and guard conflicts do not roll back external effects. A delivered ACK draft is
not a lease: the ACK tool must still validate current revision/generation,
permissions and retention. Native qualification and downstream idempotency remain
separate responsibilities.

Portable tests cover bounded paging, no implicit progress, restart/replay,
arrival, timeout, native permission loss, grant revocation, storage failure,
concurrent acknowledgment/retirement/expiry, cancellation and drain. The pinned
real Codex acceptance also checks consumer-name-based resume after restart,
arrival while polling, explicit ACK arguments, replay, empty timeout and
uninitialized/expired recovery guidance with zero native writes or model turns.
