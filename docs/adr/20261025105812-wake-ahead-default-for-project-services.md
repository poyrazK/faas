# ADR-20261025105812: Wake-ahead is on by default for project services under the project policy

- **Status:** accepted
- **Date:** 2026-10-25
- **Amends:** ADR-977 (service wake-ahead, opt-in and off by default)
- **Decision:** An app that has never set wake-ahead has it **on** when it is a production member of a project (`project_id` set, not a PR preview) whose service policy is `project` (ADR-20261022111119). Every other app keeps the off default.
  - An explicit choice always wins: `gregale wake-ahead <app> off` (or `PUT … {"enabled": false}`) stores a row, and the default no longer applies. The setting's existing "never set" state, no row and an empty `updated_at`, distinguishes the two, so there is no migration.
  - The gateway's reader (`ServiceWakeAheadEnabled`) computes the default in the same query from `apps.project_id`, `preview_of_slug` and `manifest->>'service_binding_policy'`. apid's `GET` and route advice report the same effective value through `state.EffectiveServiceWakeAhead`. The CLI says when `on` is the default.
  - Nothing else about wake-ahead changes. Until 20 cold wakes it wakes only declared `depends_on` services; after that, only services the app called after at least half its wakes and found parked on at least half of those calls. That's at most three per wake, never above 60% of the RAM ceiling, and with normal admission.
- **Why:** Under the `project` policy, services call siblings without declaring them, so multi-service projects form call chains between services nobody declared. A fully cold chain restores one service after another, and the latency adds up at every hop. Wake-ahead overlaps those restores, but as an opt-in nobody turned it on. The measured gating already bounds its cost: an undeclared sibling is woken ahead only after evidence that the app calls it and finds it parked.
- **Consequences:**
  - For new project services, cold chains overlap their restores once the gateways have measured them, with no setting to find.
  - Cost: a service woken ahead runs, and is billed, from that moment until it is called or parks again after its idle timeout. Before 20 wakes, a declared `depends_on` that the app does not call after waking is woken anyway and counts as `unused` in `gregale wake-ahead <app>`. Customers who don't want this turn it off per app.
  - Existing project apps whose policy is `project` get the default too on the first read after this lands, unless they set wake-ahead explicitly.
  - Rolling upgrades: an older gateway reads the old query and treats a never-set app as off, so an upgrade only adds wake-aheads. There are no wire or schema changes.
- **Rejected alternatives:**
  - **On for every app.** Standalone apps and the `declared` and `account` policies have no implied sibling graph, and their declared bindings already get wake-ahead on opt-in.
  - **Backfill rows with `enabled = true`.** That would turn the default into an explicit choice nobody made, and the row could no longer follow a later policy change.
  - **Always wake every sibling.** That costs a restore per sibling per wake. The measured edges do the same job only where it pays.
