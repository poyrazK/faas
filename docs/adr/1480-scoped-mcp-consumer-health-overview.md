# ADR-1480: Scoped MCP consumer health overview

Status: Accepted

## Context

Consumer listing exposes names, revisions and update times, while exact consumer
reads and resources expose readiness and lag. Reviewing all saved consumers in an
environment currently requires independent inspections and repeated native reads
without a common set of feed bounds or summary counts.

## Decision

Add read-only `gregale_get_environment_cache_activity_consumer_health` with app,
project, named environment, optional after and limit. Reuse consumer-list bounds:
default 20, maximum 50 entries, 2048-byte continuations and at most 100 saved
records per connection. Require encrypted durable state, current scoped read
consent and positively advertised native schedule recovery prerequisites. No new
quota, native endpoint, SQL change or migration is needed.

Capture matching records under the journal lock, filtering endpoint, account,
immutable app/project/environment and scope within the connection namespace.
Validate names, sort by name and fingerprint the complete scoped record set.
Capture fresh native feed bounds after the saved-record snapshot and validate
feed read access with one head probe shared across all consumers. If retention
overtakes that probe, use only its validated scoped expiry bounds. Derive each
consumer's lag using the existing cursor parsing and floor/head calculation;
never make one native read per consumer. Future saved cursors or invalid bounds
fail the entire report.

Return one page of consumer name, revision, update time, readiness and lag, along
with immutable target identity, generation, observation time and retention. Full
scope summary counts include total, ready, cursor_expired, with_retained_backlog
and needs_attention. Readiness counts partition total; backlog may include expired
consumers. Needs attention means retained pending events or an expired gap, counted
once per consumer. Do not sum overlapping event backlog across consumers or infer
stalled processing from ACK age. Unsaved/uninitialized names are not discovered.
Omit all processing cursors, recovery metadata, event pages and ACK drafts.

Health tokens have a distinct strictly decoded format containing version,
connection/target binding, generation, last name and scoped-record fingerprint.
They remain untrusted pagination data rather than signed grants or processing
proof. Reject another tool's token format. A scoped create or progress change
invalidates pagination with checkpoint_conflict; retirement anywhere in the
connection returns consumer_generation_conflict first. Exact no-op ACK retries
and other-scope progress changes preserve the scoped record set. Live feed bounds
are sampled anew per page; summaries repeat the full scope and must not be summed.

Recheck grant/storage health and all scoped records after native reads, including
failed ones. Successful report values carry an internal read guard that the
shared tool wrapper invokes after its final native authorization reads and before
serialization. This closes the gap in which a handler's own check could precede
additional wrapper I/O. Only guarded outputs opt in; other tools keep their
existing behavior. Never serialize guard context or hold journal locks across
native calls. Withhold the entire report and continuation on failure or conflict,
even when the changed consumer is outside the returned page.

## Consequences

Agents can review saved-consumer health and select individual watch/recovery
workflows. The overview is a sequential read observation, not a frozen feed,
retention lease, processing receipt or authorization to act. Processing, ACK,
recovery and retirement remain explicit. No saved records or health snapshots are
created, and reconnect/restart may reuse a valid token with the same encrypted
journal/key and namespace.

The tool uses ordinary short admission and observation drain and does not sample
get-tool lag metrics or start a poller. Catalog counts become 56 tools with native
writes disabled (54 read/planning and two bridge metadata mutations), 73 enabled.
Resource, template and prompt counts do not change. There is no journal-format
upgrade or new native/backend compatibility requirement beyond existing recovery
support.

Portable tests cover shared native-read cost, mixed ready/expired/backlog states,
full-scope summaries, pagination bounds, clock skew, namespace/target isolation,
malformed/foreign tokens, live feed updates, saved-record changes and restart.
In-flight tests cover out-of-page ACKs, creation, retirement, revocation, native
permission/identity loss, storage failure, retention, cancellation and drain,
including changes during the wrapper's final authorization reads. Real Codex
acceptance verifies discovery, health pagination/summary, backlog/expiry/recovery,
continuation after bridge reopen and conflict/access rejection with zero native
mutations and model requests.
