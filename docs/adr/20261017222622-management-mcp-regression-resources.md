# ADR-20261017222622: Consented regression resource snapshots and subscriptions

## Status

Accepted — 2026-10-17.

## Context

Management MCP clients can inspect and review regression actions. Existing
resource subscriptions cover releases, health and budgets, but cannot tell a
connected agent that its regression view needs refreshing. Native regression
visibility is app-wide, spans deployment environments, and excludes some
workflow states. It is not a durable incident event log.

## Decision

Expose `gregale://apps/{app}/regressions`, a read-only app-wide resource using
the existing regression tool's guarded projection. Its fixed detection lookback
is one hour (subject to native retention clamping), with the existing default
20-observation projection bound. Other selections remain available through the
tool. Do not add default/named-environment aliases that imply narrower evidence.
Require existing per-app diagnostic consent, immutable app/deployment checks
and final authorization on reads, subscription admission and each delivery.
Completion suggests only diagnostically consented apps.

Reuse explicit MCP 2026-07-28 subscriptions/listen and the existing capacity,
polling, expiry, cancellation, revocation and draining controls. Notify with
URI-only invalidations when the safe projected snapshot changes. Include a
snapshot observation timestamp in resource reads, but exclude that clock alone
from the subscription digest. Preserve detection/triage timestamps, exact
integer counters and native order as meaningful data. The shared digest uses
JSON numbers without a float64 round trip; health TTL parsing still preserves
freshness expiry. No background watch starts through discovery/reads.

Subscriptions sample current visibility, can coalesce or miss intermediate
transitions, carry no event cursor and do not survive restart. A disappeared
observation can reflect dismissal, resolution, lookback expiry or projection
ordering/truncation; it is not a confirmed recovery. Clients reread after an
invalidation and after reconnecting, then explicitly resubscribe. API or identity
failures terminate the stream instead of emitting an empty/healthy snapshot.
Legacy clients retain resource reads; the pinned Codex app-server qualifies
reads, while modern MCP SDK clients qualify subscription delivery.

## Consequences

No native API/schema/SDK or collection changes are necessary. No mutation,
model request, VM wake or deployment operation is authorized by this resource.
The bounded safe projection cannot detect changes exclusively outside its
returned prefix. Portable fixtures qualify this contract, not a production
native/provider deployment. Tests cover projection bounds, consent, immutable
identity races, invalidations, absence semantics, reconnect, cleanup, legacy
reads and real-client reads.
