# ADR-939: Route weights on app consumer rate cards

- **Status:** accepted
- **Date:** 2026-10-09
- **Decision:** An app rate card can set `route_weights`, a map from a bounded `"METHOD /template"` route label to a weight of 1..1000, with at most 50 routes. A request on a listed route counts as that many units, and every other request counts as one. Weighted units are what monthly allowances (ADR-937), tiers (ADR-938), quotes and statements measure.
- **Why:** Every request cost the same unit, so a cheap lookup and an expensive generation call were priced alike. Customers with compute-heavy endpoints, such as AI or reports, had to split them into separate apps or under-price them.
- **Consequences:**
  - **Gateway.** `gatewayd-internal` now resolves a route label for every consumer-attributed request, not only when route metrics, audit or discovery are on. The label comes from the declared template, or else the inferred identifier shape. A per-app set separate from route metrics bounds labels to 50 plus an overflow label, so turning metrics off never changes billing. The label travels in the usage outbox and `ConsumerUsageEvent.billing_route` (field 15). Anonymous traffic carries none.
  - **apid.** apid keeps `api_consumer_route_usage_minutes`, billable units per consumer, route and minute, in the same transaction as the per-minute totals, which stay authoritative. This adds one upsert per consumer request.
  - **Labels that cannot be stored.** A malformed label or the overflow label is dropped instead of rejected, so a replayed financial fact never wedges the outbox. Requests from older gateways, the legacy debugger fallback, or overflow routes have no route row and count at weight 1. Missing route data can only undercount, never overcharge.
  - **Weighting.** A minute's weighted units are its requests plus `(weight − 1) × route units` for each weighted route, capped so route rows never count more requests than the minute's total. The card effective at the minute supplies the weights.
  - **Revisions.** Weighted units only grow as usage grows, so revision coverage and adjustments (ADR-936) are unchanged. Statement `billable_units` are weighted units.
  - **No backdating.** Weights re-measure past minutes, so a card with weights cannot be backdated once any card uses allowances, tiers or weights.
  - **Platform tenant statements.** Cross-app statements reject app cards with weights (422), as they do for allowances and tiers.
- **Rejected alternatives:**
  - Weighting at the gateway would need every gateway to hold rate cards and would freeze weights into the immutable ledger, so changing a price would not re-weight unbilled usage.
  - Adding the route to the per-minute total's key would change every reader of the ledger and its aggregates.
  - Billing by measured guest duration instead was left for a separate compute meter; weights are predictable and auditable from request counts.
