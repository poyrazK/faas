# ADR: gregale.yaml owns an app's event subscriptions, with per-plan caps

Date: 2026-10-31
Status: Accepted
Related: [ADR 20261029093000](20261029093000-event-ga-gate.md) (event GA gate), [ADR 20261027125625](20261027125625-events-kill-switch.md) (events kill switch)

## Context

Event subscriptions are created only from `event_triggers` in `gregale.yaml`.
A blocker hunt on 2026-10-31 found three problems with them.

1. **A subscription is never removed.**
   - A deploy only inserts declarations and re-enables existing ones
     (`UpsertEventSubscription` forces `enabled = true`).
   - Nothing ever disabled a subscription. `DeleteEventSubscription` ran only
     when a failed deploy rolled back, and there was no API to delete one.
   - So removing a trigger kept it delivering forever.
   - The filter is part of a subscription's identity, so editing a filter
     created a second live subscription, and events matching both filters
     were delivered twice.
2. **There is no limit on the number of subscriptions.**
   - Every publish gathers every matching enabled subscription of the
     account inside the publish transaction, and schedd matches each one
     again.
   - With no cap, one account could make every publish expensive on the
     shared database, and finding 1 made the pile grow with every deploy.
3. **Numbers in filters were rounded.** Filters were normalized through
   float64, so a filter on an integer above 2^53 was stored rounded and
   matched the wrong events.

The owner chose (2026-10-31):

- disable undeclared subscriptions rather than delete them;
- reconcile only on deploys that carry a manifest;
- stop routing events already accepted for a disabled subscription;
- cap subscriptions like crons.

## Decision

1. **Reconcile on production deploys.**
   - A deploy that applies its `gregale.yaml` with triggers, at the
     production scope, makes the manifest's `event_triggers` the app's whole
     set. Every enabled subscription it does not declare is disabled
     (`SetEventSubscriptionEnabled`), and an empty or missing list disables
     them all.
   - A disabled subscription that is declared again is enabled again: same
     row, history and controls.
   - Declared subscriptions are matched by row id after their upsert, not by
     filter text, so number formatting cannot disable a declared one.
   - A rollback restores both changes, re-enabling what the deploy disabled
     and disabling what it re-enabled.
2. **What does not reconcile.** These only add subscriptions, as before:
   - stage-scope deploys;
   - deploys without a manifest or with `--no-triggers`;
   - batch trigger creation (`POST .../triggers:batch`).
3. **Routing re-checks.** Admission of an application recipient that has no
   delivery yet reads the subscription. If it is disabled or gone, the
   recipient settles as filtered with `filter_reason: subscription_disabled`:
   no invocation, no retry. Deliveries already created still run. Events
   published after the disable never capture the subscription, because the
   publish trigger selects only enabled ones.
4. **Caps.** `pkg/api/limits.go` gains `EventSubscriptionsPerApp` and
   `EventSubscriptionsPerAccount`, the same shape as the cron caps:

   | Plan | Per app | Per account |
   |---|---:|---:|
   | Free | 0 | 0 |
   | Hobby | 5 | 10 |
   | Pro | 20 | 50 |
   | Scale | 100 | 500 |

   The account count covers the apps of the app's billing subject, the cron
   quota subject.
   - **When checked:** after the deploy has written and disabled its
     subscriptions. Of two concurrent deploys in one account, the later count
     sees both, so no lock is needed.
   - **Over a cap:** the deploy fails with `403
     plan_event_subscription_quota` (limit and observed value) and rolls back.
   - **Shrinking is allowed:** the check fails only a deploy that added or
     re-enabled a subscription, so an app already over a cap can still deploy
     a smaller set.
5. **Exact filters.** Filter numbers are decoded as `json.Number`, so they
   are stored with every digit.

## Consequences

- Removing or changing a trigger takes effect at the next production deploy.
  The first such deploy after this change also disables any duplicate or
  leftover subscriptions that piled up before it.
- Events in flight for a disabled subscription are filtered, not delivered.
  If the trigger is restored later, they are not redelivered; replay or
  republish covers that.
- Per-publish routing work is bounded by the account cap.
- A subscription stored with a rounded filter before this change keeps it:
  the deploy matches declarations to rows in float64, as before, so the
  rounded row still counts as declared. Removing the trigger in one deploy
  and adding it back in the next stores it exactly. Large-integer filters
  are rare.
- Filters on large integers now match exactly.
- No migration. Rows keep their ids. An older apid keeps the old additive
  behaviour, and an older schedd keeps routing disabled subscriptions'
  in-flight events until it is upgraded.
