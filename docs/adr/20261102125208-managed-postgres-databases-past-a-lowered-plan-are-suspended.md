# ADR-20261102125208: Managed PostgreSQL databases past a lowered plan are suspended, not deleted

- **Status:** accepted (owner, 2026-11-02: "suspend, not delete")
- **Date:** 2026-11-02
- **Owner:** apid (managed PostgreSQL) maintainers
- **Extends:**
  - [ADR-20261025094915](20261025094915-managed-postgres-compute-caps-on-running-databases.md): the compute cap sweep also holds a database the plan no longer includes;
  - [ADR-20261031091607](20261031091607-managed-postgres-ga.md): the GA tier's downgrade check covers the downgrades no one asks for.

## Context

- **A voluntary downgrade is checked first.** `planDowngradeProblem` refuses it while the account's managed PostgreSQL databases exceed the target plan's database count or use a service class it does not include (#6277).
- **Other downgrades are not checked.**
  - When a subscription ends, the account drops to Free (`DowngradeEndedSubscription`).
  - A plan changed in the billing provider's portal, or by an operator, applies without the check.
- **Usage already follows the new plan.** Each cap sweep reads the subject's current plan, so its compute ceiling and storage pool drop at once.
- **Count and class do not.** They are checked only when a database is created or resized.
  - A Scale account whose subscription ends keeps ten databases on Free, which allows one.
  - Its production-class databases keep their larger computes, which Free does not include.
  - Gregale pays the provider for every one of them.

## Decision

1. **Which databases are past the plan.** A subject's databases are ranked oldest first, by creation time and then ID. A database is past the plan when either holds:
   - it ranks past the plan's `DatabasesMax`;
   - the plan does not include its service class.

   Some databases are not ranked:
   - a restore holding the restore slot: [ADR-20261031123259](20261031123259-managed-postgres-restore-slot.md) already ends it;
   - a database that is deleting, or has no provider resource yet.

   A subject whose plan is unknown has nothing past it. `managedpostgres.PlanExcess` is the one implementation.
2. **A database past the plan is suspended.**
   - The compute cap sweep holds its provider quota at its own use. This is the mechanism that suspends a database at the compute ceiling, so its compute stops and cannot start.
   - Nothing else changes: data, credentials, bindings and restore history stay. Its storage still counts toward the pool.
   - A provider quota scope that also holds a database inside the plan is not suspended; the sweep logs it. This is a restore inside its source's project.
3. **Nothing is deleted.**
   - Every sweep recomputes the set. When the subject is back inside its plan, the next sweep gives the database its ordinary quota again. The owner gets there by upgrading, deleting a database, or resizing it to a class the plan includes.
   - Suspension only stops compute: the storage held keeps costing Gregale. A time limit on keeping it is an open item.
4. **Switch.**
   - `FAAS_MANAGED_POSTGRES_PLAN_EXCESS_ENFORCEMENT` takes `off`, `shadow` (the default) or `on`.
   - Shadow logs each database it would suspend. `on` writes the quota, and only while the compute cap (`FAAS_MANAGED_POSTGRES_CAP_ENFORCEMENT`) is `on` too. With the compute cap off, nothing runs.
   - Each sweep counts databases past the plan, scopes suspended and shared scopes left running (`apid_managed_postgres_plan_excess_*`).
5. **Telling the customer (steps 2 and 3).**
   - Step 2: the database view gets a `plan_excess` reason (`database_limit` or `service_class`), shown by the CLI and the dashboard.
   - Step 3: the owner gets an email that lists the suspended databases and how to resume them.
   - Production keeps the switch at `shadow` until step 2 ships.

## Consequences

- **Every way of losing a plan now reaches the databases, within one usage sweep:** an ended subscription, a portal change and an operator change.
- **A suspended database's connections fail.** The application sees the provider's error; the view (step 2) and the email (step 3) explain it.
- **Nothing a customer stored is lost by a downgrade.** Only the open item can change that.
- **Mixed scopes are rare.** A restore that shares its source's project is the only case, and it stays counted toward the ceiling.

## Delivery

1. **The sweep (#6361).**
   - The ranking and the suspension in the cap sweep, behind the switch at `shadow`.
   - Metrics, the environment contract, and the operator guide.
2. **The view.** `plan_excess` on the database list and read, the CLI's `list` and `get`, and the dashboard, shown while the switch and the compute cap are both `on`. It is ranked with the same `PlanExcess` the sweep uses.
   Production can turn the switch `on` in the release that ships it.
3. **The email.** The sweep records a notice per suspended database (`managed_postgres_plan_excess_notices`) and drops it once the database is back inside the plan. apid mails each notice once through the producer outbox (template `managed_postgres_plan_excess`), behind `FAAS_INTERNAL_MAIL_MANAGED_POSTGRES_PLAN_EXCESS_OUTBOX_ENABLED` on apid and maild (off by default).
