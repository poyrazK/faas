# ADR-1368: MCP consumer resources and change notifications

Status: Accepted

## Context

Named activity consumers support inspection, review, watch, baseline recovery
and explicit acknowledgment. MCP resource clients can read and subscribe to
release, health and budget state, but cannot yet observe consumer readiness or
pending work through that interface.

## Decision

Add the generic resource template
`gregale://projects/{project}/environments/{environment}/apps/{app}/activity-consumers/{consumer}`.
Require a complete named selector, a valid consumer name, encrypted durable state,
current native read access and durable grant consent. Reuse consumer inspection
and its immutable identity, retention, revision and generation validation.
Expose consumer/target identity, readiness, saved revision/generation and update
time, and point-in-time lag diagnostics. Omit saved/recovery cursors, recovery
metadata, hints, event pages and ACK drafts. Resource reads and polling do not
sample the get-tool lag metrics and never create or advance saved progress.

Extend existing resource subscription handling rather than adding another
transport or polling service. Modern clients opt in through subscriptions/listen;
legacy clients retain resource reads without subscription support. Existing
stream/resource limits, short-request admission, bounded polling/expiry,
notification coalescing and lifecycle cleanup apply unchanged. No new quota or
API bound is introduced.

Consumer semantic digests retain readiness, revision, generation, backlog,
expired-history gap and retention. Remove observation time, ACK age and its
clock-skew flag from the digest only; direct reads still expose those diagnostics.
An uninitialized consumer has no chosen feed position and therefore no invented
backlog notifications. Retirement is meaningful through the rotated generation,
including other subscribed consumers in the same connection. Saved consumers and
generation changes remain isolated by connection namespace and scoped binding.

Before releasing a resource or notification, recheck durable grant/store health
and captured revision/generation after the final native consent/identity reads.
Never hold journal locks during native I/O. Poll and recipient checks discard
observations that conflict with a concurrent ACK or retirement and retry later;
direct reads and initial subscriptions fail with the existing conflict code.
Permission/identity loss, revocation, unavailable storage and API errors end the
affected listen. Notifications contain only the canonical URI and SDK metadata.
No resource contents or actionable progress are broadcast.

Add completion for the exact consumer URI template, reusing the bounded scoped
consumer-name completion and requiring the full selector. Discovery advertises
eight generic resource templates and starts no native reads or polling.

## Consequences

Compatible clients can notice that consumer state needs attention and then reread
the resource or explicitly call watch/recovery tools. Sampled notifications can
coalesce or miss intermediate transitions; they are not an event feed, processing
lease or downstream completion proof. Processing and ACK remain explicit. Streams
do not survive reconnect/restart and must be subscribed again.

Portable SDK tests cover both supported read protocols and session/HTTP transports,
read-only journal behavior, resource completion, meaningful changes versus clock
noise, ACK/recovery/retirement, connection isolation, busy retries, in-flight
conflicts, revocation, permission/identity/storage loss, expiry, disconnect, drain
and explicit resubscription. Real Codex acceptance reads uninitialized, ready and
expired resources and checks persistence after bridge reopen, without native
writes or model requests. The pinned Codex app-server has no resource-subscribe
method; notification delivery is verified with the standard modern MCP SDK.

There are no native endpoints, SQL changes, migrations, journal-format changes,
new management tools, automatic acknowledgments or background subscriptions
without an explicit client request.
