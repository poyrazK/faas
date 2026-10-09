# ADR-651: Due automation backlog alerts

- **Status:** accepted
- **Date:** 2026-10-07
- **Extends:** ADR-725 workflow alert signals and ADR-730 queue health
- **Decision:** Add `workflow_due_age_seconds` to the existing customer alert
  rule system, backed by current durable workflow state. Seed the opt-in
  `automation_backlog` reliability preset for Hobby and higher plans. It
  notifies when the oldest due automation run has waited at least 300 seconds,
  with a 30-minute default cooldown. Catalog availability does not enable a
  rule or configure a recipient for any customer.
- **Eligibility:** Include due pending runs, elapsed parked wakes and expired
  running leases, including the legacy lease fallback. Start age at the wake
  or lease expiry, bounded below by creation time. Exclude future schedules,
  retry backoff and intentional waits, live running claims, terminal runs and
  native Customer Operations custody. Capacity-blocked due runs still count.
  Return zero when no automation work is due. Memory alerts and queue health
  share their eligibility calculation; PostgreSQL predicates are checked
  against the health response in store tests.
- **Scope and time:** App rules cover all automations and tenant scopes in that
  owned app. Existing account-wide rules aggregate owned apps. Age represents
  current state, independently of `window_spec`; it is not a lookback average
  or a persisted duration for a continuously true queue-depth condition.
  Preserve the existing pending and waiting age metrics. In particular,
  waiting age continues to include intentional waits.
- **Delivery:** Reuse signed webhook delivery, atomic fire claims, audit,
  cooldown, retry and receiver idempotency. A sustained breach may notify again
  after cooldown. Clearing the queue returns the rule to `ok` and records its
  resolution through the existing evaluator. No separate recovery webhook is
  introduced. Failed or unavailable observations degrade the rule without
  firing; PostgreSQL signal reads have a five-second deadline. Keep the
  metric notification-only in API validation, database constraints and
  rollback capture. It cannot trigger promote, demote or rollback actions.
- **Privacy:** Return the existing aggregate alert payload. No run input,
  output, error text or tenant identity enters this alert. Use automation
  health and run history to investigate the affected app.
- **Rollout:** Apply the additive vocabulary and catalog migration, then
  update every evaluator/API replica and clients before enabling the preset.
  Older evaluators do not understand the new metric. Remove rules using it
  before downgrading; the down migration refuses to discard customer rules.
  Existing presets and rules survive rollback. There is no new persistent
  run state or VM lifecycle change.
