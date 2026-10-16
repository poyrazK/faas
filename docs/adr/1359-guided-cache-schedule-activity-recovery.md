# ADR-1359: Guided MCP cache schedule activity recovery

Status: Accepted

## Context

Consumers must coordinate capturing a checkpoint, paginated baseline discovery,
retention checks and activity catch-up. Capturing a head by draining the feed is
unbounded, and silently replacing it during discovery can skip concurrent changes.

## Decision

Expose a read-only native checkpoint endpoint using the existing ledger bounds
in one repeatable-read snapshot, without event scanning or additional SQL/schema.
Return the existing scoped recovery metadata shape, including an initial zero
checkpoint and fully pruned ledger bounds.

Add one read-only MCP recovery tool. Capture the head before the first baseline
page and preserve it across every continuation. Return one bounded page per call,
with a versioned opaque token containing endpoint, account, immutable target,
checkpoint and native schedule continuation. Tokens are untrusted pagination
metadata like native feed cursors, not signed attestations or grants. Validate
both cursors and current consent on every call; never accept caller-supplied
filters, target changes or another API endpoint. No server-side session, secrets,
action bodies or stored authorization is introduced.

Check the original activity checkpoint before and after every discovery page,
using bounded reads that discard events without advancing the checkpoint. If
retention overtakes it, withhold the page and return recovery_restart_required
with validated recovery metadata. Callers discard the partial baseline and start
without a token; the tool never splices a newer checkpoint into old discovery.

The final page returns the original activity cursor for subsequent catch-up and
watch calls. Cancellation and transient errors do not commit server-side progress;
retry the same input continuation, including across MCP reconnects/restarts.

## Consequences

Recovery performs bounded native reads per page, independent of feed backlog.
There is live pagination and at-least-once overlap, not a frozen snapshot: refresh
exact status for replayed schedule identities. Concurrent earlier-sorting schedules
are covered by activity replay. Removed history and gateway convergence remain
outside baseline completion. Page and token limits are centralized in api/limits.go.
Deploy updated apid readers before MCP helpers. No additional migration is needed;
existing durable-feed and retention migrations remain required.
