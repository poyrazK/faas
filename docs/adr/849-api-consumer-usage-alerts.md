# ADR-849: Usage alerts for app consumers

- **Status:** accepted
- **Date:** 2026-10-10
- **Decision:** A consumer plan (ADR-847) can list up to five `alert_thresholds_percent`: percentages from 1 to 100 of its `max_units_per_month`. A threshold's unit count is `ceil(limit × percent / 100)`.
  - **Recording.** When a gateway admission takes a consumer's monthly counter from below that count to at or above it, the same admission transaction inserts a row into `api_consumer_usage_alerts`. The row is unique per consumer, UTC month and threshold, and the transaction also enqueues a `consumer.usage_threshold` app webhook through `app_webhook_event_outbox`.
  - **Denials.** A request denied by the monthly limit records the 100% threshold, if the plan has one. A weighted request can be refused before usage lands exactly on the limit.
  - **Reconciliation.** `GET /v1/apps/{slug}/consumers/{consumer_id}/usage-alerts` lists recorded crossings.
- **Why:** Plans cap consumers at the edge, but nothing warned a customer's user before requests started returning 429. API sellers need an upgrade or top-up prompt before the cap, not a support ticket after it.
- **Consequences:**
  - **Same counter as enforcement.** Alerts measure the admission counter the gateway enforces, so "80%" means 80% of what the 429 counts. They therefore exist only for plan-assigned consumers whose plan has a monthly unit limit; thresholds without one are rejected with 422. The billing ledger can differ slightly, for example for platform failures that are admitted but not billed (ADR-843). Alerts are about the cap, not the bill.
  - **Owner and cost.** `gatewayd-internal` already owns `api_consumer_plan_admissions` writes. It now also inserts alert and outbox rows, only in the transaction that crosses a threshold. Monthly denials of plans with a 100% threshold run one extra `INSERT … ON CONFLICT DO NOTHING` per denied request. The webhook dispatcher's background drain delivers outbox rows, so the gateway needs no new dependency.
  - **Exactly once per month.** The unique key makes a crossing once per consumer, month and threshold, even with concurrent replicas; the admission row lock serializes them. Changing a plan's thresholds or limit mid-month does not re-send alerts already recorded. Lowering the limit can cross several thresholds on the next request, and each fires.
  - **Plan changes.** An alert names the plan in force when it fired. A consumer moved to another plan mid-month keeps the month's counter, which counts across plans (ADR-847). Crossings already recorded for a percentage are not re-sent under the new plan that month.
  - **Update semantics.** Plan limits are replaced on update. `alert_thresholds_percent` is optional on update: omitted keeps the thresholds and `[]` clears them, so older clients that only send limits keep their alerts.
  - **Clone registry.** The new column and table are registered with the environment-clone schema registry. The table is operational state, not configuration.
- **Rejected alternatives:**
  - Evaluating alerts in apid from the billing ledger would warn on a different number than the cap enforces, and needs a per-minute scan or a new running total.
  - A periodic evaluator would add delay and a new job for a signal the admission path already computes.
  - Absolute-unit or amount thresholds without a monthly cap are left for later. They need counting for unlimited consumers, which admission skips today.
