# ADR-20261030214500 · Gregale Observe review, round 3: incidents outlive their rule, cascaded rule deletes are audited, preset tests are rate-limited

- **Status:** proposed
- **Date:** 2026-10-30
- **Builds on:** [ADR-20261029203000](20261029203000-observe-review-round-2.md)
  (Observe review, round 2), [ADR-20261022193512](20261022193512-unified-incidents.md)
  (unified incidents and notes), [ADR-747](747-custom-slos-error-budgets.md),
  [ADR-748](748-synthetic-checks.md), [ADR-749](749-alert-notification-channels.md)
- **Decision:** A third code review of Gregale Observe found incident history
  lost with its rule, rule deletes nobody audited, and an outbound send with
  no per-account cap:
  1. **An alert incident outlives its rule.** `alert_episodes.rule_id`
     referenced `alert_rules` with `ON DELETE CASCADE`, and `incident_notes`
     cascades from the episode, so deleting a rule — directly, through the
     SLO or synthetic check it watches, or with its app — erased its
     incidents, their acknowledgements and their notes: the postmortem
     record went with the thing being investigated. The foreign key is
     dropped. A `BEFORE DELETE` trigger on `alert_rules` copies the rule's
     name, metric and app into new `alert_episodes` columns (`rule_name`,
     `rule_metric`, `app_id`) and resolves any open episode at the delete,
     so one trigger covers every delete path. Episode reads left-join the
     rule and fall back to the copied columns; a deleted rule's episode
     reports an empty rule state.
  2. **A deleted rule's episodes are purged after the delivery retention.**
     The evaluator's delivery purge (ADR-20261029203000) also deletes,
     oldest first and in the same bounded batches, episodes whose rule is
     gone and that resolved before `AlertDeliveryRetentionDays`; their notes
     go with them. A live rule's episodes are kept, as before.
  3. **Rules removed with an SLO or a synthetic check are audited.**
     Deleting either cascades to the alert rules built on it, and only the
     SLO or check delete was audited. apid lists those rules first and, once
     the delete succeeds, records `alert_rule.deleted` for each, with
     `cause` (`slo` or `synthetic_check`) and `cause_id`. The in-memory
     store now cascades these deletes to the rules too, as PostgreSQL does.
  4. **Preset test sends are rate-limited per account.** `POST
     .../alert-presets/{name}/test` posts to a real webhook, like a channel
     test, but had no cap. It now allows
     `AlertPresetTestsPerAccountMinute` (10) per account per minute and then
     answers 429 `alert_preset_test_rate_limited` with `Retry-After`; channel
     and preset tests share one limiter implementation.
  5. **Synthetic probes are documented as traffic.** A probe goes through the
     public edge, so it counts in request analytics, request, error-rate and
     latency alerts, and availability and latency SLOs; the docs now say so
     and how to tell probes apart (`ua_family`).
- **Compatibility:** the migration is **contract**: older apid binaries
  inner-join episodes to rules, so they skip a deleted rule's episodes
  instead of failing, but the migration ships only with a full release. The
  `Down` restores the cascading foreign key, deleting any orphaned episodes
  first. The purge and the audit events need no flag.
- **Consequences:** a deleted rule's incidents stay readable for
  `AlertDeliveryRetentionDays` after they resolve. Audit consumers see one
  `alert_rule.deleted` per rule an SLO or check delete removed.
