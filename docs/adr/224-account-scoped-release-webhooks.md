# ADR-224 · Account-scoped release webhooks

- **Status:** accepted
- **Date:** 2026-09-23
- **Decision:** Let one account-owned subscription receive release lifecycle events from its current and future apps. Keep app-owned subscriptions unchanged and use the existing signed delivery ledger, retry policy, dispatcher, and dead-letter path.
- **Why:** Per-app registration and secret rotation do not scale for a platform managing many apps, and new apps can miss the release receiver.
- **Consequences:** Account subscriptions have no app ID, accept only four release events, and count against the existing per-account webhook quota; public creation and fan-out are staged behind this storage PR.
- **Rejected alternatives:** Copying subscriptions to every app, a second dispatcher, and an unbounded account-wide all-events filter.
- **Supersedes:** none; extends ADR-076.

## Context

ADR-076 stores one subscription per app. The deployment and rollout producers now publish `deployment.live`, `deployment.failed`, `rollout.completed`, and `rollout.aborted`. A platform operating many Gregale apps must register and rotate the same receiver once per app, and must remember to register it again for every new app. That is an integration hazard rather than an application preference.

## Decision

`app_webhooks` gains a closed `scope` of `app` or `account`. Existing rows default to `app`; an `app` row retains its required app ID and existing `(app_id, target_url)` uniqueness. An `account` row has no app ID and is uniquely identified by `(account_id, target_url)` within that scope. Its account ID remains a foreign key to the owner account. Account rows require a non-empty filter containing only the four release events above; an empty all-events filter would silently expand its authority as new event producers ship.

The delivery remains app-shaped even for an account subscription: `app_webhook_deliveries.app_id` names the source app, while `webhook_id` names the account subscription. Later fan-out must establish `apps.account_id = app_webhooks.account_id` in the same transaction as the source transition. A foreign account must neither receive a delivery nor learn that an app exists. One event delivered to both an app subscription and an account subscription produces two intentional delivery IDs; retries keep each ID stable.

The existing `WebhookPerAccount` plan limit is the total budget for both scopes. Account-scoped creation must lock the account row, count both kinds of subscriptions, and apply that limit before insertion. App creation must count account subscriptions too. No new plan quota is introduced.

## Staging

1. This PR establishes the replay-safe storage shape, constraints, and nullable-app read compatibility. It exposes no public creation route and does not fan out to account rows.
2. A follow-up adds account-owned CRUD and release-event fan-out, with account isolation, quota, filter, disabled-subscription, and duplicate-transition tests. The four release producers retain their transactional enqueue behavior.
3. Client surfaces and an end-to-end signed receiver test complete the feature. The public route must not be exposed before fan-out works.

## Consequences

The dispatcher and delivery ledger remain singular. App-scoped URLs and semantics do not change. Deleting an app still cascades its app-scoped subscriptions; deleting an account cascades its account-scoped subscriptions. Account receivers are not tied to the lifecycle of any one app.

## Rejected alternatives

- **Copy one app subscription to each app:** creates secret-rotation drift and misses future apps.
- **A second account-webhook ledger and dispatcher:** duplicates signing, retries, fairness, and DLQ state machines.
- **All-events account filter:** creates an unbounded future privacy and volume commitment.
- **One delivery for matching app and account subscriptions:** each subscription owns its own signing secret, retry policy, and delivery history.
