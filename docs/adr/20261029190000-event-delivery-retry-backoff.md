# ADR: Event deliveries retry with backoff and honour the app's retry policy

Date: 2026-10-29
Status: Accepted
Amends: ADR-134 (durable invocation retries), ADR-614 (event delivery backpressure)
Related: [ADR 20261029093000](20261029093000-event-ga-gate.md) (event GA gate)

## Context

Event routing creates each subscription delivery as an async invocation
with no retry policy (`pkg/state/event_routing_admission.go`). The drain
therefore retried a failed delivery at its fixed producer default, every
5 s, until the plan's `MaxQueueAttempts` (Hobby 3, Pro 10, Scale 25), then
dead-lettered it:

| Plan | Attempts | Dead-lettered after |
|---|---:|---|
| Hobby | 3 | about 10 s |
| Pro | 10 | about 45 s |
| Scale | 25 | about 2 minutes |

A downstream outage longer than that dead-lettered every event in flight,
and a burst of failures hammered the handler every 5 s. A customer's
`retry_policy` in `gregale.yaml` (the app's retry default) paced broker
trigger records but not event deliveries, and `retry:` on an
`event_triggers` entry configures routing retries, not the handler.

The owner chose (2026-10-29) to back off by default and honour the app's
policy, keeping the plan's attempt caps.

## Decision

When a failed invocation is an event delivery (it carries the
`x-gregale-event-subscription-id` header that routing stamps on first
deliveries and replays) and has no retry policy of its own, the drain paces
it by:

1. the app's `retry_policy` when the app sets one, its `max_attempts` still
   capped by the plan;
2. otherwise the event delivery backoff in `pkg/api/limits.go`: 5 s
   doubling to 5 minutes, ±20 % jitter, with the plan's attempts.

The first retry stays at today's 5 s, so no plan retries sooner than
before:

| Plan | Attempts | Dead-lettered after |
|---|---:|---|
| Hobby | 3 | about 15 s |
| Pro | 10 | about 20 minutes |
| Scale | 25 | about 95 minutes |

The policy is resolved on the failure path only, where the drain already
reads the app for the attempt budget, so routing and the first dispatch do
no extra work. It also applies to deliveries already pending when a
scheduler with this change starts. Other invocations keep their own policy
or the producer default.

## Consequences

- A downstream outage of up to 20 minutes (Pro) or 95 minutes (Scale) no
  longer dead-letters events; replay is still there for longer ones.
- A failing handler sees retries spread out instead of every 5 s.
- Customers who need a different curve set `retry_policy` in
  `gregale.yaml`; a per-subscription handler retry setting is not added.
- No schema change. An older scheduler keeps retrying event deliveries
  every 5 s; both run side by side.
