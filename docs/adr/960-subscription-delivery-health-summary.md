# ADR-960: Subscription delivery health summaries

## Status
Accepted.

## Context
Routing health and execution health already expose different parts of delivery. Operators need a single view to identify pending work, retained retries/dead letters, subscription controls, and concrete oldest pending blockers without confusing routing admission with handler success.

## Decision
Add an authenticated, account/app-scoped read-only `GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-health` and CLI `events subscription-delivery-health`. Compose existing routing health, bounded execution health, and one oldest-first pending application-recipient backlog page under the existing subscription control deadline. Use the existing window values. All component reads must succeed; failed reads return an error rather than zero-filled healthy evidence. Set no-store and reuse app-read scopes and MFA.

Preserve separate component observation timestamps, assembly start/end timestamps, and coverage flags. Reads are independent snapshots and may observe concurrent delivery changes. Routing pending/processing counts cover current live recipients. Execution counts include retained handler replays and backfills, are bounded, and always have incomplete historical coverage. The requested window affects recorded outcome metrics, not current execution state totals. Add optional latest_successful_completion_at to execution health: the maximum valid completion timestamp among successful bounded retained executions, no earlier than acceptance or later than that read's observation time. It is not an all-time watermark and is not limited to the requested metrics window.

Bound the pending sample at centralized EventConsumerDeliveryHealthSampleMax=50. Sample only pending application recipients, retain exact acceptance identity and blocker evidence, and expose truncation. Waiting-reason counts describe sampled recipients only. The first sampled recipient is the oldest under backlog's existing acceptance/outbox/subscription ordering. An absent sample/completion is missing retained evidence, not proof of no historical work. Configured rate, pause, and circuit state describe controls; actual blocker evidence comes from pending recipients. No overall healthy verdict is calculated.

## Consequences
One API/CLI/SDK view spans routing and handler evidence without adding SQL, changing delivery behavior, or mutating incident checkpoints. Consumers must inspect coverage and component timestamps before comparing counts. Retention and concurrent changes can make component counts differ. Existing execution-health callers gain an optional timestamp. No compliance registry changes or new recovery actions are introduced.
