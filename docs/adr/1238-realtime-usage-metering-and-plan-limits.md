# ADR-1238: Realtime usage metering and plan limits

- **Status:** draft. Rollout steps 1 and 2 are implemented (usage is recorded, not billed). The billed meters, limits and prices are proposed; the per-plan numbers are left blank for the owner to fill in from `ex44_faas_financial_model.xlsx`.
- **Date:** 2026-10-14
- **Decision:** Meter managed realtime per endpoint (connections and messages), roll the counts into the existing hourly billing-meter pipeline, deliver them in shadow mode for one billing period, then enforce per-plan connection and message limits from `pkg/api/limits.go`.
- **Why:** Realtime is the only managed service with no usage meter and no plan limit beyond an endpoint count. Its costs (realtimed memory per connection, Postgres writes per retained message, fan-out bytes) grow with use that no bill or quota sees.

## Context

What exists today:

- **Plan limits.** Endpoint counts only: Free 0, Hobby 2 per app / 10 per account, Pro 10 / 50, Scale 25 / 250. The table was `realtimeLimits` in `pkg/api/realtime.go` rather than `pkg/api/limits.go`, as the repository rule requires; step 1 moved it.
- **Connections.** Each endpoint's `max_connections` is customer-set, up to `RealtimeMaxConnections` (10,000) on every plan, and defaults to 10,000. No account-wide cap exists, so a Hobby account can hold 10 endpoints × 10,000 connections.
- **Messages.** Retained history is bounded per endpoint (32 channels × 1,024 messages × 4 KiB, 24 h). Live publish and fan-out have no plan limit.
- **Usage data.** realtimed's counters (`realtimed_current_connections`, `realtimed_sent_messages_total`, `realtimed_sent_bytes_total`, …) are process-wide, with no account or endpoint label. realtimed is a host daemon, so its bytes are not in the per-instance egress meter (`usage_minutes.net_tx_bytes` comes from VM taps).
- **Billing pipeline.** `BillingMeterWindow` rows (account, UTC hour, meter, quantity) feed provider delivery. Only `compute` and `egress` exist. Providers already support a `shadow` delivery mode.
- **Realtime triggers (ADR-1237).** Invocations bill as compute (plan RAM + 8 MB per running second). They must not also count as realtime messages.

## Decision

### Meters

realtimed keeps per-endpoint counters in memory, samples them every second into per-hour totals, and every minute sends each realtimed process's **cumulative** totals for the current UTC hour to apid over the existing private `RealtimeFleet` gRPC service. apid stores them in `realtime_usage_hours (endpoint_id, hour, node_name, incarnation, account_id, app_id, minute_peak_connections[60], connection_seconds, delivered_messages, delivered_bytes, received_messages, received_bytes)`:

- A report keeps the larger of the stored and reported value for every column, so repeats and out-of-order delivery cannot double-count or lower a row. A process restart gets a new `incarnation`, so it starts new rows instead of overwriting the old run's totals.
- `account_id` and `app_id` come from the endpoint row at ingest, never from the report, and have no foreign keys: usage outlives endpoint deletion until retention.
- The fleet peak for an hour is the largest per-minute sum of the nodes' peaks, so connections on different nodes in the same minute add up and a single spike is not double-counted across minutes.
- A completed hour realtimed cannot deliver is retried for `RealtimeUsageReportMaxAge` (24 h), then dropped and counted in `realtimed_usage_dropped_hours_total`.
- Delivered counts cover frames carrying a customer payload (v1 messages, v2 `message`, `direct_message`, `inbox_message` and `signal` frames, SSE `message` events), one per recipient. Received counts cover client messages forwarded to the app. Retained-channel publishes are already countable from channel heads, so realtimed does not count them.
- Rows age out with `usage_minutes` (13 months) in meterd's retention loop.

Billed meters, added beside `compute` and `egress`:

| Meter | Quantity per hour | Notes |
|---|---|---|
| `realtime_connections` | **owner choice:** peak concurrent connections, or connection-minutes | See question 1 |
| `realtime_messages` | delivered messages (one per recipient) | Publishes to retained channels count once more at write; trigger deliveries do not count |

`delivered_bytes`, `received_messages` and `received_bytes` are recorded for capacity planning, not billed.

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

1. **Done.** Move `realtimeLimits` into `pkg/api/limits.go`. No behaviour change.
2. **Done, off by default.** Per-endpoint counters, minute reports and hourly rows; recorded only. Enable with `FAAS_REALTIME_USAGE_ENABLED=1` on realtimed.
3. Billing meters in shadow mode; usage shown in the dashboard and `gregale usage`.
4. After the shadow period: live delivery, the connection cap, and message quotas.

## Consequences

- Customers see realtime usage before they are billed for it.
- apid upserts one row per active endpoint per realtimed process each minute; the table grows by one row per active endpoint per process per hour.
- Admission takes a soft dependency on fleet counts: if counts are stale for more than 30 s, a node admits against its local count only and alerts, rather than refusing all connections.
