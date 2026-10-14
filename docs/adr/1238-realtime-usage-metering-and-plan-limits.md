# ADR-1238: Realtime usage metering and plan limits

- **Status:** draft. The meters and enforcement points below are proposed; the per-plan numbers and prices are left blank for the owner to fill in from `ex44_faas_financial_model.xlsx`. Nothing here is implemented.
- **Date:** 2026-10-14
- **Decision:** Meter managed realtime per endpoint (connections and messages), roll the counts into the existing hourly billing-meter pipeline, deliver them in shadow mode for one billing period, then enforce per-plan connection and message limits from `pkg/api/limits.go`.
- **Why:** Realtime is the only managed service with no usage meter and no plan limit beyond an endpoint count. Its costs (realtimed memory per connection, Postgres writes per retained message, fan-out bytes) grow with use that no bill or quota sees.

## Context

What exists today:

- **Plan limits.** Endpoint counts only: Free 0, Hobby 2 per app / 10 per account, Pro 10 / 50, Scale 25 / 250. The table is `realtimeLimits` in `pkg/api/realtime.go`, not in `pkg/api/limits.go` as the repository rule requires.
- **Connections.** Each endpoint's `max_connections` is customer-set, up to `RealtimeMaxConnections` (10,000) on every plan, and defaults to 10,000. No account-wide cap exists, so a Hobby account can hold 10 endpoints × 10,000 connections.
- **Messages.** Retained history is bounded per endpoint (32 channels × 1,024 messages × 4 KiB, 24 h). Live publish and fan-out have no plan limit.
- **Usage data.** realtimed's counters (`realtimed_current_connections`, `realtimed_sent_messages_total`, `realtimed_sent_bytes_total`, …) are process-wide, with no account or endpoint label. realtimed is a host daemon, so its bytes are not in the per-instance egress meter (`usage_minutes.net_tx_bytes` comes from VM taps).
- **Billing pipeline.** `BillingMeterWindow` rows (account, UTC hour, meter, quantity) feed provider delivery. Only `compute` and `egress` exist. Providers already support a `shadow` delivery mode.
- **Realtime triggers (ADR-1237).** Invocations bill as compute (plan RAM + 8 MB per running second). They must not also count as realtime messages.

## Decision

### Meters

realtimed keeps per-endpoint counters in memory and flushes one row per endpoint per minute to `realtime_usage_minutes (account_id, app_id, endpoint_id, minute, peak_connections, connection_seconds, published_messages, delivered_messages, delivered_bytes)`, keyed for idempotent upsert so a retried flush cannot double-count. Each node writes its own rows; the hourly roll-up sums nodes, and takes the per-minute sum of node peaks as the fleet peak.

Billed meters, added beside `compute` and `egress`:

| Meter | Quantity per hour | Notes |
|---|---|---|
| `realtime_connections` | **owner choice:** peak concurrent connections, or connection-minutes | See question 1 |
| `realtime_messages` | delivered messages (one per recipient) | Publishes to retained channels count once more at write; trigger deliveries do not count |

`delivered_bytes` and `published_messages` are recorded as telemetry for capacity planning, not billed.

### Plan limits

New rows in `pkg/api/limits.go` (and `docs/plans.md`), replacing the separate `realtimeLimits` table:

| Limit | Free | Hobby | Pro | Scale |
|---|---|---|---|---|
| Endpoints per app / account (existing) | 0 / 0 | 2 / 10 | 10 / 50 | 25 / 250 |
| Concurrent connections per account | — | _owner_ | _owner_ | _owner_ |
| Messages included per month | — | _owner_ | _owner_ | _owner_ |
| Overage, millicents per 1,000 messages | — | _owner_ | _owner_ | _owner_ |
| Overage, millicents per connection unit | — | _owner_ | _owner_ | _owner_ |

An endpoint's `max_connections` is clamped to the account's connection limit at create and update.

### Enforcement

- **Connections.** realtimed admits a connection when the account's fleet-wide count is under the limit. Each node publishes its per-account counts every 5 s; admission uses the last fleet total plus the node's local change since then, so overshoot is bounded by nodes × 5 s of accepts. A refusal is HTTP 429 with an RFC 7807 body (`code: realtime_connection_limit`, limit, observed value, docs URL).
- **Messages.** Paid plans accrue overage and respect the account's overage cap (ADR-1162), like compute. If a plan has no overage, publishing past the included amount returns 429 `realtime_message_quota`; already-open connections stay open.
- Enforcement ships only after one full billing period of shadow metering shows the numbers are right.

## Questions for the owner

1. Bill connections by **peak concurrent per month** (simple to explain; one spike sets the month) or **connection-minutes** (tracks cost; harder to predict)?
2. Included amounts and overage prices per plan, consistent with the financial model.
3. Does any plan get a hard stop at its included amount instead of overage?
4. Length of the shadow period (default proposed: one full calendar month).

## Rejected alternatives

- **Sample process-wide Prometheus counters.** They have no account attribution and reset on restart.
- **Bill realtime bytes as egress.** The egress meter is per instance and its price reflects VM traffic; realtime fan-out bytes are a different cost, and mixing them hides both.
- **Count trigger deliveries as messages.** The invocation is already billed as compute; counting it again double-charges the same work.

## Rollout

1. Move `realtimeLimits` into `pkg/api/limits.go`. No behaviour change.
2. Per-endpoint counters, the minute flush and the hourly roll-up; telemetry only.
3. Billing meters in shadow mode; usage shown in the dashboard and `gregale usage`.
4. After the shadow period: live delivery, the connection cap, and message quotas.

## Consequences

- Customers see realtime usage before they are billed for it.
- realtimed writes one row per active endpoint per minute per node.
- Admission takes a soft dependency on fleet counts: if counts are stale for more than 30 s, a node admits against its local count only and alerts, rather than refusing all connections.
