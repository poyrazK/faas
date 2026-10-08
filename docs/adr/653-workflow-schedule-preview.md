# ADR-653 · Read-only workflow schedule preview

- **Status:** accepted
- **Date:** 2026-10-07
- **Extends:** ADR-726 bounded catch-up and ADR-637 tenant-configurable schedules
- **Decision:** Add account and tenant-self read endpoints plus `gregale workflows schedules preview` and Go, Node, and Python SDK methods. Preview upcoming timezone-aware fire times and the next catch-up decision.
- **Why:** Schedule inspection shows the effective cron expression and next fire but does not explain DST adjustments or how a delayed evaluator treats missed occurrences.
- **Consequences:** Preview calls use the published effective trigger, tenant override when applicable, and durable cursor. `at` and `since` allow bounded what-if evaluation; cursor state is never advanced. Scheduler and preview share the same nominal catch-up calculation. Fire times carry local offsets and identify spring-gap shifts. Fixed wall-time expressions run once in a repeated fall hour; interval expressions follow cron interval behavior. A new or changed schedule still arms before its next admitted occurrence. Predictions do not reserve concurrency or establish worker availability.
- **Rejected alternatives:** Accepting an arbitrary trigger definition would preview a definition different from the deployed schedule. Creating runs or advancing a cursor during simulation would turn an inspection into an admission side effect.
