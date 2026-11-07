# ADR-20261104132753: Bytes sent without an instance count as egress, and builds have a monthly quota

- **Status:** accepted (owner, 2026-11-04: "lets solve those issues", on the round 5 cost-control audit)
- **Date:** 2026-11-04
- **Owner:** gatewayd-internal, meterd, apid and realtimed maintainers
- **Amends:**
  - [ADR-20261103131742](20261103131742-billing-identity-plan-fit-and-egress-ceiling.md): the egress ceiling and egress overage count the new edge and realtime bytes.
  - [ADR-1323](1323-realtime-usage-metering-and-plan-limits.md): fan-out bandwidth is now attributed to the app. Realtime still has no meter or price of its own.
  - [ADR-20261104085150](20261104085150-log-volume-allowance.md): delivered drain bytes count as the app's egress.
- **Implements:** the 2026-11-04 owner decision "builds: free, with a per-plan monthly build-minute quota".

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

App egress (the monthly allowance, the ceiling an unbilled plan stops at, and
the overage when egress is billed) counted only `usage_minutes.net_tx_bytes`.
That value is the bytes an instance's network interface sent. Gregale also
sends bytes for an app without an instance:

- **Response cache.** A cache hit, a stale-while-revalidate serve, a
  stale-while-waking serve and a stale-if-error serve all come from the
  gateway. Cache rules are not behind a flag: Hobby has 1 per app, Pro 5 and
  Scale 20. Each entry can be up to 1 MiB, and hits are limited only by the
  account's request rate (Hobby 1,200 a minute). So a Hobby app could serve
  about 51 TiB a month against a 100 GiB ceiling, without being billed.
- **Fixed edge responses.** These are respond rules, up to 64 KiB.
- **Log drains.** A drain sends from Gregale's network. After
  ADR-20261104085150 a Hobby app with 25 drains could still send about 2 TiB
  a month.
- **Realtime fan-out.** ADR-1323 left it "not attributed to accounts".
  realtimed already measures delivered bytes per endpoint
  (`realtime_usage_hours`), but prod did not record them.

Separately, the owner decided on 2026-11-04 that builds are free but have a
monthly build-minute quota per plan. Before this record, builds were bounded
only by deploys per hour, the 15-minute build timeout and the builder's
per-account fairness window. At those limits one Free account could queue
about 150 build-minutes an hour.

## Decision

### 1. Edge bytes are the app's egress

- **The edge usage ID.** The gateway records bytes it serves for an app
  without an instance against the app's edge usage ID,
  `md5('gregale-edge-egress:' || app_id)` as a UUID
  (`api.EdgeUsageInstanceID`). It uses `EgressSink.RecordEdgeBytes`.
- **What counts.** Response bodies from the cache, stale serves and respond
  rules, and the line bytes of each delivered drain record. A HEAD response
  sends no body and records nothing.
- **The frames.** The records go out on the existing durable
  `EgressTxService` stream. Each frame carries the new `app_id`, and its
  `instance_id` is the edge usage ID.
- **Persisting them.** meterd stores an edge frame with
  `AppendGatewayEdgeUsageEvent`. That uses the same per-node event
  idempotency table, then upserts `usage_minutes` under the edge usage ID,
  setting `tx_bytes` and `net_tx_bytes` and no compute.
- **What follows from the row.** The existing triggers bind the payer,
  record egress billing evidence and update the monthly totals. So the bytes
  count toward the allowance, the ceiling and the overage exactly as an
  instance's do.
- **A deleted app.** A frame for an app that no longer exists is recorded
  and dropped, so it cannot block the stream.

### 2. Realtime delivered bytes are the app's egress

- **Recording on.** apid and realtimed run with
  `FAAS_REALTIME_USAGE_ENABLED=1`.
- **The sync.** On every quota tick, before it reads usage, meterd runs
  `SyncRealtimeEgressUsage` under a transaction advisory lock. It appends to
  each app's realtime usage ID (`md5('gregale-realtime-egress:' || app_id)`)
  the month's delivered bytes minus what earlier ticks appended.
- **Why a difference is safe.** `realtime_usage_hours` values only grow
  (`greatest()`), so the difference is exact.
- **The month boundary.** A report for last month's final hour that arrives
  after the month ends is not counted.

### 3. Builds have a monthly quota

- **The quota.** `api.Limits.BuildMinutesPerMonth` is the builder time per
  UTC month of the account that pays for an app. The payer is the
  organization when it owns its resources, as `bind_app_usage_owner` decides.

  | Plan | Build minutes / month |
  |---|---:|
  | Free | 200 |
  | Hobby | 1,000 |
  | Pro | 3,000 |
  | Scale | 6,000 |

  The owner set these numbers on 2026-11-07 (amendment 1).
- **What counts.** Each build of the payer's apps that started this month,
  from `started_at` to `finished_at`, or to now while it runs. A coalesced
  build ran no builder of its own and does not count.
- **Where it is enforced.** `apidsource.Enqueue` checks the quota for every
  source build: CLI, tarball, Git, GitHub push, previews, project apply and
  retries. A build past the quota is refused with
  `403 plan_limit_build_minutes`, which carries the limit, the observed
  minutes and a docs link. A prebuilt image deploy builds nothing and is not
  refused.
- **Failure mode.** A failed read admits the build. The quota bounds shared
  builder time, and an outage of its read must not stop every deploy.

### Release compatibility

- **meterd asks for edge frames.** It sets `StreamBytesRequest.edge_frames`
  only when it can persist frames. The gateway sends edge records only to a
  stream that asked and keeps them pending for the next one. So an older
  meterd, which would treat an `instance_id` without an instance as an error
  and replay forever, never receives one.
- **Older gateways.** Edge records keep a valid `instance_id`, so an older
  gateway still loads a ledger that holds them. If such a gateway sends one
  without `app_id` after a rollback, meterd cannot persist it; rolling a
  gateway back below this release should wait until its ledger has drained.
- **Deploy order** stays data, then control plane (meterd and apid), then
  edge and node.

## Consequences

- **What egress means.** A plan's egress now covers everything Gregale
  sends for an app: instance traffic, edge responses, drains and realtime.
  An unbilled plan stops at its ceiling whichever path sent the bytes. A plan
  that bills egress bills them all.
- **What customers see.** Customers see these bytes in usage exports and the
  egress meter as their app's traffic. The plans page says what counts.
- **Builds.** Builds stay free. A heavy builder meets a clear,
  limit-carrying error and can still deploy images.
- **Open follow-ups.**
  - Response headers are not counted, only bodies.
  - The log archive still keeps every line for 90 days (ADR-20261104085150).

## Amendment 1 (2026-11-07): owner's numbers, and a build counts at most the build timeout

- **Numbers.** Free 200, Hobby 1,000, Pro 3,000, Scale 6,000 build minutes a
  month (were assumed at 300 / 1,000 / 3,000 / 10,000).
  - Production over the last 30 days: 503 successful builds, median 78 s,
    p95 248 s, max 541 s. The heaviest account used about 120 minutes.
  - One builder slot is about 43,000 minutes a month. Scale at 6,000 keeps
    one account under 14% of the guaranteed slot.
- **A build counts at most `BuildTimeoutSeconds` (15 minutes).** It ends at
  `finished_at`, else `cancelled_at`, else now, capped at `started_at` plus
  the timeout.
  - Production has cancelled builds with no `finished_at`. Counted to now,
    three of them added about 69,000 minutes to one Scale account in a
    month, which would have refused all of its builds.

