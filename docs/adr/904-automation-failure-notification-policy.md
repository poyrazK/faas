# ADR-904: Automation failure notification policy

- **Status:** accepted
- **Date:** 2026-10-09
- **Extends:** ADR-045 customer alerts and ADR-725 workflow alert signals
- **Decision:** Seed the opt-in `automation_failures` reliability preset for
  Hobby and higher plans. Use the existing `workflow_failures` signal with
  comparison `gte`, threshold one, a five-minute observation window, and a
  thirty-minute default cooldown. Catalog availability does not create rules
  or configure recipients. Reuse existing API and CLI policy management.
- **Scope:** The preset covers all workflows and tenant scopes in the selected
  owned app. Failed and dead runs count by terminal time; cancellations,
  successful runs, active runs, and intermediate retry attempts do not count.
  Existing custom account-wide rules retain their aggregate scope. This is
  not a per-run delivery guarantee: failures during cooldown can be coalesced
  and can leave the observation window before the next eligible notification.
- **Delivery:** Preserve atomic fire claims, signing, audit history, delivery
  retries, and receiver idempotency. Repeated ticks during cooldown do not
  enqueue duplicates. A breach that clears returns the rule to `ok`; no
  separate recovery webhook is introduced. Unavailable signal reads degrade
  the rule without firing. Workflow metrics remain notification-only and
  cannot trigger deployment actions or automatic pauses.
- **Investigation:** App-scoped workflow alerts include authenticated relative
  API paths to workflow run lists and automation definitions after checking
  app ownership. Failure alerts supply separate failed and dead run paths.
  These paths show current history, not the alert's exact observation window.
  Do not filter by run creation time, because long-running work can finish
  inside the failure window. App lookup failure omits links without blocking
  delivery. Account-wide notifications omit app context.
- **Privacy:** Retain aggregate counts and rule metadata. Add only the owned
  app ID and investigation paths; never copy inputs, outputs, error messages,
  webhook secrets, or tenant identities into the notification.
- **Rollout and rollback:** Apply the additive preset vocabulary and seed
  migration, then update evaluators for the optional investigation metadata.
  Existing evaluators already understand the failure metric. Replaying the
  migration preserves catalog customizations. Rolling back removes the
  preset while preserving existing rules and delivery history: the original
  failure metric remains supported. No new persistent run or VM state exists.
