# ADR-20261031091607: Managed PostgreSQL for general availability

- **Status:** accepted
- **Date:** 2026-10-31
- **Owner:** apid (managed PostgreSQL) maintainers
- **Amends:** spec §16 ("Postgres for customers … post-GA"), [ADR-155](155-provider-neutral-managed-postgres.md), [ADR-20261023181931](20261023181931-rollbacks-that-restore-the-database.md) (its capability), [ADR-20261028103115](20261028103115-managed-postgres-production-provisioning.md) (rollout)
- **Decision:** Managed PostgreSQL ships as a GA product in the next release. In `pkg/productcap/catalog.json`:
  - **`ga`:**
    - `managed-postgres` (create, get, list, delete, recover, usage, `gregale add postgres`, `gregale.yaml` databases);
    - `postgres-bindings` (attach, bindings, rotate);
    - `postgres-restore` (point-in-time restore into a new database).

    Their docs page is `/docs/postgres`. The first two list every plan, Free included, because ADR-1430 gives Free one database. `postgres-restore` lists only Pro and Scale. A restore is a new database, so it needs room under the plan's database limit, and Free and Hobby allow one database.
  - **`beta`:**
    - `postgres-compute-policy` (resize and the scale-to-zero policy);
    - `org-postgres` (organization databases; organizations are beta).
  - **Unchanged:**
    - `postgres-cutovers`, `data-api` and `live-queries` stay `preview`;
    - `postgres-overage` stays `internal`.
  - **The `postgres` CLI command moves to the customer audience,** with the summary "Create, attach and restore managed PostgreSQL databases".
    - `postgres cutover` now belongs to `postgres-cutovers`, and `postgres live` to `live-queries`, so the CLI still hides those preview verbs from accounts they are not released to.
    - In the command tree, `postgres live` joins the operator verbs under `admin postgres` (sealed live queries stay an operator preview; customers read live through `data-api live`).
  - **`/docs/postgres` and `/docs/managed-postgres` are published.** `docs-links-check` refuses an unpublished docs page for a GA capability.

  **Rollbacks that restore the database become their own capability.** `postgres-restore` also owned the `database-switches` routes, the `rollback switch` commands and `restore_databases` on `POST /v1/apps/{slug}/rollback`. Promoting it as it was would have released ADR-20261023181931's switches, which are still proposed and have no end-to-end drill on a real provider. They now belong to `database-rollbacks`, which is `preview` with parent `postgres-restore`.

  **What GA promises:**
  - **Plans.** The ADR-1430 allowances (databases, pooled storage, monthly CU-hours, a 7-day restore window and service classes per plan). They are limits, not billed overage, as the pricing principle requires: a resource-holding feature gets a plan limit, not a meter.
    - Compute is enforced by the cap enforcer (ADR-20261025094915) and storage by the storage pool (decision 9).
    - Free suspends at its allowance. Paid accounts suspend at their allowance too, because the overage setting stays `internal` until managed PostgreSQL is invoiced.
  - **Availability.** `single_zone` only, with no HA claim. The SQL probe (ADR-20261029172812) pages the platform when no apid can write and read a canary database.
  - **Data safety:**
    - point-in-time restore inside the plan's window, on Pro and Scale;
    - a 7-day undo for a customer delete (ADR-20261029174950);
    - for operators, Neon's own 7-day project recovery after a purge.
  - **Versions.** New databases choose PostgreSQL 15–18; 14 is retiring (it stays available to existing databases).
  - **Scale.** The fleet database cap (`max_databases`) stays at or below the Neon plan's project limit. That is about 1,000 on Scale, so one Neon organization serves about 1,000 databases.

  **Acceptance evidence.** `tests/journeys/postgres_test.go::TestManagedPostgres` is the acceptance test for `managed-postgres` and `postgres-restore`. It runs against the deployed product (full tier) and does what a customer does:
  - create a database and attach it to an app;
  - write and read rows through the app;
  - restore to a point between two writes and read the restored copy;
  - delete the database (scheduled), recover it with its rows, and purge it.

  It uses a fixture app with a dependency-free PostgreSQL client, so the build needs no package registry.

  **Rollout in the release.** The GA default turns the `managed_postgres` flag on for every account without a stored definition. The owner tests on production before customers see it, so in this order:
  1. **Before the release reaches production,** store the flag closed except for the owner's account:
     - `gregalectl flags default --key managed_postgres --value false --reason "GA rollout" --yes`
     - `gregalectl flags allow --key managed_postgres --account-id <owner account> --reason "GA rollout" --yes`
  2. **Qualify the Neon backend** in an isolated project: `managed-postgres-qualify` with `--compute-cap`, `--storage-cap` and the deletion hold check (ADR-20261029174950 decision 2).
  3. **Turn on enforcement and the probe:**
     - `FAAS_MANAGED_POSTGRES_CAP_ENFORCEMENT=on` and `FAAS_MANAGED_POSTGRES_STORAGE_CAP_ENFORCEMENT=on`, each after its shadow readout;
     - the cap emails (`FAAS_INTERNAL_MAIL_MANAGED_POSTGRES_CAP_OUTBOX_ENABLED=1` on apid and maild);
     - the SQL probe canary databases.
  4. **Approve the production backend** (ADR-20261028103115). This covers the backend pin and fingerprint, an approval window, and fleet ceilings sized to the Neon plan.
  5. **Run the journey** against production with the owner's account: `scripts/e2e-journeys.sh --tier full --run TestManagedPostgres`.
  6. **Open it to every account at the public release,** once the public-release gate below is green: `gregalectl flags default --key managed_postgres --value true --reason "GA" --yes`.

  **Rollback.** `gregalectl flags disable --capability managed-postgres --reason … --yes` hides managed PostgreSQL and its sub-features for every account. Existing databases keep running, and credentials already delivered keep working. Closing the production approval stops new databases, bindings, restores and resizes without hiding anything.
- **Public-release gate** (owner, 2026-10-31: managed PostgreSQL is a core feature of Gregale and launches GA with the public release). The code ships GA now. Step 6 waits for the P0 workstreams of the managed PostgreSQL epic, [#6146](https://github.com/poyrazK/faas/issues/6146):
  - **PG-01: lifecycle evidence.** The journey green on production, extended to credential rotation, resize, suspend and wake, and restart recovery. Its result is recorded in `docs/STATUS.md`, and the qualification evidence is part of the release checks.
  - **PG-02: sustained production use.** A Scale account includes 200 CU-hours a month. A production-class database never scales below 1 CU, so under steady traffic it spends that in about 8 days and is then suspended. Paid overage stays `internal` until it is invoiced. Before the public release, PG-02 must deliver:
    - a priced continuation. The owner chose both (2026-10-31):
      - billed overage at ADR-1430's rates ($0.20 per CU-hour, $0.60 per GB-month), released through the existing overage setting and its 2x ceiling;
      - an always-on add-on for steady production databases. Its price is not in the financial model yet, so it needs that decision and its own ADR;
    - customer spending controls;
    - invoicing that reconciles provider usage.
  - **PG-03: customer recovery.** Restore on every plan (ADR-20261031123259, #6142). The earliest and latest recoverable points are shown. At least one supported restore-and-switch path, through database-aware rollback or qualified cutover activation, is measured against stated RPO and RTO.
- **Open at acceptance** (follow-ups outside the public-release gate):
  1. **Restore on one-database plans.** Free and Hobby cannot restore, because the restored database would be their second. ADR-20261031123259 (#6142) proposes a restore that may hold one database above the limit for 7 days. Restoring in place, in the same database, is a later follow-up.
  2. **A copy outside Neon.** There is no logical backup off the provider. A lost Neon project or organization loses its databases.
  3. **Invoicing.** Nothing past the allowances is billed, so overage stays `internal`. Billing it needs an invoice line and a financial-model decision.
  4. **Restore-history cost.** Neon bills instant restore at $0.20 per GB-month of history. A database with heavy writes costs more history than its storage limit suggests, and no plan limit bounds it yet (Neon's written-data quota is the candidate).
  5. **More than about 1,000 databases.** This needs a second Neon organization behind the provider-neutral backend, or an agreement with Neon.
- **Why:**
  - The GA blockers from the 2026-10-23 review are closed in code:
    - production provisioning and fleet ceilings (ADR-20261028103115);
    - account deletion (ADR-20261024150500);
    - compute caps, emails and the storage pool (ADR-20261025094915);
    - the SQL probe (ADR-20261029172812);
    - the delete undo window and usage waiver (ADR-20261029174950);
    - the Neon review fixes: usage collection pacing, background pacing, one sweeper, the hold check and PostgreSQL 14.
  - What remains is operator work in the release (above) and the follow-ups listed.
  - The spec's lean ("stay a compute company until Gate C") predates ADR-1430, which put managed PostgreSQL in every plan of the financial model. This ADR records that the product is sold now, as plan allowances, not as a separate price.
- **Consequences:**
  - **Flags.** A stored definition wins over the tier default, so step 1 must land before the release reaches production. Accounts with an operator-written `managed_postgres` or `postgres_restore` rule keep it.
    - Design partners who used database-restoring rollbacks through `postgres_restore` need `database_rollbacks` allowed as well.
  - **SDKs.** `sdk/openapi.yaml` and the Node and Python SDKs gain the `/v1/postgres` paths of the three GA and two beta capabilities.
  - **Docs.** The customer guide (`docs/postgres.md`) is published and no longer says preview. The operator guide (`docs/managed-postgres.md`) gains the GA rollout.
- **Rejected alternatives:**
  - **Promote every Postgres sub-capability.** Cutovers, the Data API and live queries have no live-path evidence, and overage has no invoice.
  - **Keep restore in preview.** Restore is the customer's recovery path from their own mistakes. A database product without it at GA would ask customers to trust a 7-day undo alone.
  - **Wait for an off-provider backup.** That needs storage, a schedule, a restore path and pricing. Neon keeps point-in-time history and 7 days of project recovery, and the owner chose to ship with this gap recorded.
