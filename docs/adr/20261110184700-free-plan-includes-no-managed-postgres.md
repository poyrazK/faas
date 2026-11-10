# ADR-20261110184700: The Free plan includes no managed PostgreSQL, and databases past their plan are deleted after 30 days

- **Status:** accepted (owner, 2026-11-10: "Can we remove database service from free tier?", then "yes, do both with 30 days", on the billing launch review). Slice 1 built.
- **Date:** 2026-11-10
- **Owner:** apid and managed PostgreSQL maintainers
- **Amends:**
  - [ADR-1430](1430-plan-entitlements-from-october-2026-cogs-report.md): Free no longer includes one development database.
  - [ADR-20261102125208](20261102125208-managed-postgres-databases-past-a-lowered-plan-are-suspended.md): a database suspended past its plan is no longer kept forever. After 30 days it is deleted through the delete grace period.

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

- **Free databases cost real capacity.**
  - Production managed PostgreSQL is Neon, where each database is a Neon
    project. The organization holds about 1,000 projects.
  - The fleet's paid reserve (ADR-20261031091607) keeps 20% of them for paid
    plans, so Free databases could fill the other 80%. After that, every new
    Free account was refused (`ErrFleetFreePaused`).
  - Nothing reclaimed an idle or abandoned Free database. Its storage stayed
    on Neon's bill forever.
- **Free was already closed in production.** apid refused Free databases
  unless compute caps were enforced (bug hunt 33 H1). Production runs caps in
  `shadow`, so a Free account could not create a database, yet the plans
  table and the docs still offered one.
- **Databases past a lowered plan were kept forever.** When a subscription
  ends, the account falls to Free and the cap sweep suspends the databases
  the plan no longer includes. It keeps their data, credentials and bindings
  with no end date. Each one still holds a Neon project.

## Decision

### Slice 1: Free includes no managed PostgreSQL

- `api.ManagedPostgresPlanLimits` gains `NotIncluded`. Free's entry is
  `{NotIncluded: true}`: no databases, storage, compute or service class.
  `api.ManagedPostgresIncluded` reports whether a plan includes any.
- Every create path refuses a plan that includes none with
  `403 managed_postgres_not_in_plan`, whatever the cap enforcement mode:
  - the create route;
  - `gregale dev --postgres`;
  - a Compose `postgres` service and a manifest database;
  - the service's admission, which covers restores, snapshot copies and
    pipeline mirrors.
  Admission no longer skips its plan check while caps are `on` and the usage
  policy is disabled.
- `PlanExcess` treats a `NotIncluded` plan as one with every ranked database
  past it (reason `database_limit`). An unknown plan, the zero value, still
  suspends nothing. So an account that falls to Free has all its databases
  suspended, not left uncapped.
- The capability catalog no longer lists Free for managed PostgreSQL, its
  bindings, point-in-time restore or the Data API. The customer and operator
  docs say Free includes no database.
- The fleet's paid reserve stays. It no longer binds anyone today, but it
  keeps protecting paid plans if a plan without overage gains databases.

### Slice 2: databases past their plan are deleted after 30 days

- **Clock.** The clock is the database's plan excess notice. The sweep writes
  `recorded_at` when it first suspends the database past its plan, keeps it
  while the database stays past the plan, and clears it once the database fits
  again: after an upgrade, a resize to an included class, or deleting another
  database.
- **Deletion.** Once `recorded_at` is 30 days old (`api.ManagedPostgresPlanExcessDeleteAfter`),
  the sweep schedules the database's delete through the delete grace period
  (ADR-20261029174950):
  - its compute stays held;
  - the owner can undo the delete for 7 more days;
  - after that the reconciler purges the provider resource.
  An undone delete returns the database to ready while it is still past the
  plan. The next sweep suspends it again and starts a new 30-day clock.
- **Notices.** The suspension email says when the database will be deleted.
  A reminder goes out 7 days before.
- **Rollout.** `FAAS_MANAGED_POSTGRES_PLAN_EXCESS_DELETE=off|shadow|on`
  defaults to `shadow`, which logs the databases it would delete. `on` takes
  effect only while the plan excess sweep itself is `on`.
- **Scope.** Deletion covers only databases past their plan. A paid
  database inside its plan is never deleted for inactivity.

## Consequences

- No Free account holds a Neon project. The project count tracks paid
  accounts, plus databases past their plan for at most 37 days.
- A Free account sees managed PostgreSQL as an upgrade. Hobby is the
  entry plan for a database.
- Any production Free database from before this change, for example one an
  account flag allowed, is suspended at the next enforced sweep. Slice 2
  deletes it 30 days later.
- `ex44_faas_financial_model.xlsx` must not count Free databases. The
  owner updates the sheet if it does.
