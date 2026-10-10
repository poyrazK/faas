# ADR-938: Named consumer plans

- **Status:** accepted
- **Date:** 2026-10-09
- **Decision:** An app can define up to 20 named consumer plans, such as `free`, `pro` and `enterprise`. A plan bundles two things:
  - **limits:** admitted requests per consumer per minute, and weighted units per consumer per UTC month;
  - **prices:** its own rate-card history, made of cards created with `plan_id`.

  Consumers move between plans through append-only assignments that take effect from a UTC minute. App-wide cards, those with no plan, remain the default plan, so existing apps and statements are unchanged.
- **Why:** Pricing (ADR-935..937) and enforcement (throttles, tenant budgets) were configured separately, and prices applied to every consumer of an app. Selling a free tier with a hard cap, or moving one customer to a better price, needed manual coordination of several resources.
- **Consequences:**
  - **Pricing.** Statements and quotes price each minute with the plan in force at that minute. The price-history timeline replaces each assignment segment with the plan's card history, or the default cards, starting at the segment boundary. The existing quoting code then applies unchanged: allowances, tiers and weights keep counting through the month across plan changes, and statement lines keep the original card IDs.
  - **No backdating.** Assignments cannot be backdated, and the target plan must have a card in force at the effective minute. Plan cards cannot be backdated either. Together these keep billed minutes from being re-priced.
  - **Enforcement.** `gatewayd-internal` reads each consumer's plan policy through a cache that lives 15 seconds: the current plan's limits and its current card's route weights. Consumers on unlimited plans cost one cached lookup and no write. For a limited plan, the gateway admits the request against one locked counter row per consumer, `api_consumer_plan_admissions`, following the tenant-budget pattern (ADR-240). A request counts 1 toward the per-minute limit and its route weight toward the monthly cap, so the cap measures the same units as billing.
  - **Denials.** Over-limit requests get 429 with `Retry-After` and `x-faas-rate-limit-scope: consumer-plan-minute|month`. Unverifiable admission gets 503. Neither is billed.
  - **Cap versus bill.** Like tenant budgets, admissions are counted when a request is admitted. The cap may therefore count a request that later fails on the platform and is not billed (ADR-934).
  - **Plan limits** change in place and reach the gateway within 15 seconds; prices change only through new card versions.
  - **Platform tenant statements** reject apps whose cards belong to plans (422), as they do for allowances, tiers and weights.
- **Rejected alternatives:**
  - Per-consumer price overrides without named plans would scatter the same tariff across many consumers and make "move everyone on pro to the new price" a bulk edit.
  - Starting a new plan at the next month boundary was rejected because upgrades should take effect immediately.
  - Process-local token buckets for plan limits were rejected because a hard cap must hold across replicas.
  - Billing counters for the cap were rejected because ledger delivery is asynchronous, so caps enforced from billed usage would overshoot.
