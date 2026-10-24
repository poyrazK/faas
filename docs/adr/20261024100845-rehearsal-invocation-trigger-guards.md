# ADR-20261024100845 · Rehearsal triggers on invocations skip work that cannot matter

- **Status:** accepted
- **Date:** 2026-10-24
- **Amends:** [ADR-20261019120000](20261019120000-rehearsal-runtime-hot-path-isolation.md)
- **Decision:** Three of the four rehearsal triggers on `invocations` now run less for apps that are not rehearsing:
  - `scenario_suspend_dispatch_guard` gains a `WHEN` that matches a claim.
  - `scenario_postgres_fault_scope` is split: its insert half keeps the trigger, and its update half moves to `scenario_postgres_fault_scope_dispatch` with the same `WHEN`.
  - `scenario_managed_work_capture` reads the policy of `NEW.app_id` a second time on an update only when the update moved the row to another app.

  Behaviour is unchanged.
- **Why:** These triggers run for every app's invocations, enrolled or not. ADR-20261019120000 left them in place because the guarantees they hold need the database.

## Measurement

The test ran 2,000 invocation lifecycles (insert, claim, completion), each committed on its own, for an app with no rehearsal policy. It used `track_functions = 'pl'` on a migrated PostgreSQL 16 database.

- **Before:** the four rehearsal trigger functions took 8.1% of all trigger self time on `invocations`, about 5% of the lifecycle. Calls per lifecycle were three to `scenario_managed_work_capture`, three to `scenario_postgres_fault_scope`, two to `scenario_suspend_dispatch_guard` and one to `scenario_suspend_work_guard`.
- **After:**
  - Calls fell from nine to seven per lifecycle.
  - Lookups of `scenario_test_action_policies` fell from about ten to seven.
  - Buffer accesses per lifecycle stayed within noise, about 300, because a lookup in an almost empty policy table reads about one page. The saving is in PL/pgSQL calls, not I/O.
  - Wall-clock time on the shared development machine varied by more than the expected saving, so the gain was not timed.

The same measurement found that the largest trigger costs on `invocations` are outside rehearsal:

| Trigger function | Share of trigger self time |
|---|---|
| `guard_retired_workload_execution` | 19.6% |
| `record_invocation_attempt_history` | 17.7% |
| `sync_invocation_schedule_occurrence` | 16.0% |
| `invocation_backlog_count` | 10.3% |

## Decision

| Trigger | Before | After |
|---|---|---|
| `scenario_suspend_dispatch_guard` | Every update of `state`, `attempts` or `instance_id` | Only when `NEW.state = 'dispatching'` and one of the three changed. That is exactly the condition the function tests first. |
| `scenario_postgres_fault_scope` | Insert, and every update of `state`, `attempts` or `instance_id` | Insert only |
| `scenario_postgres_fault_scope_dispatch` (new) | | The claim update, with the `WHEN` above. It runs the same function. |
| `scenario_managed_work_capture` | Two policy lookups on every update | One lookup, unless `app_id` changed |

On the fault scope's non-claim updates, the old update half took the run's advisory lock and returned. An app with a fault plan always has a managed-work plan, and `scenario_managed_work_capture` runs first and takes the same lock on every update. So dropping those calls changes no serialisation.

`scenario_postgres_fault_scope_dispatch` sorts directly after `scenario_postgres_fault_scope`, so the BEFORE triggers keep their order. The migration uses `CREATE OR REPLACE TRIGGER`. That takes the same `SHARE ROW EXCLUSIVE` lock as `CREATE TRIGGER`, rather than the `ACCESS EXCLUSIVE` lock `DROP TRIGGER` would take on this busy table.

## Consequences

- A rehearsal still costs every invocation write about seven policy lookups and seven PL/pgSQL calls. Folding the remaining triggers into one dispatcher would cut that to one each. It would mean rewriting four guard bodies, one of which is shared with other tables, so it is left for a measured need.
- The non-rehearsal costs above are larger. They belong to their owners' areas.
