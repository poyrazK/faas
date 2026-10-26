# Managed PostgreSQL compute caps hold on running databases

Date: 2026-10-25
Status: Proposed
Amends: ADR-1430 (plan entitlements from the October 2026 COGS report), ADR-155 (provider-neutral managed PostgreSQL)
Related: ADR-492 (consumption contract), ADR-20261023115741 (every capability behind a platform flag)

## Context

ADR-1430 gives each plan a monthly managed PostgreSQL compute allowance, pooled
across the account's databases: Free 5, Hobby 25, Pro 75 and Scale 200
compute-unit hours (CU-h). It calls these ceilings, because managed PostgreSQL
has no invoicing. Today the ceiling only stops *new* work:

- The usage ledger is off unless the operator turns on the `usage` block
  (`UsagePolicy.Enabled`).
- Even when it is on, `AdmitWithCeilings` only refuses creates, restores and
  resizes. A database that is already running is never stopped.
- The Neon adapter sets only `settings.quota.logical_size_bytes` on a project.
  Neon has no compute limit, so a Free database can run all month.

A hard stop at the allowance is not a fix on its own. A database with steady
traffic never scales to zero, so it uses at least its smallest size around the
clock:

| Plan | Allowance | Smallest size | Days of steady traffic before it stops |
| --- | --- | --- | --- |
| Free | 5 CU-h | 0.25 CU | under 1 |
| Hobby | 25 CU-h | 0.25 CU | about 4 |
| Pro | 75 CU-h | 0.25 CU | about 12 |
| Scale (production class) | 200 CU-h | 1 CU | about 8 |

Neon can enforce a monthly compute limit itself. A project's
`settings.quota.compute_time_seconds` caps compute seconds per billing month.
When a project reaches it, Neon suspends every compute in the project and keeps
them suspended until the quota is raised above the project's usage, or until
the counters reset on the first day of the next month. That suspension is
persistent, unlike a scale-to-zero suspend, which a new connection wakes.

## Decision

The owner decided the policy on 2026-10-25.

1. **Free stops at its allowance.** A Free account's databases are suspended
   once the account has used its monthly allowance.

2. **Paid plans have an overage setting, off by default.** It is a per-account
   setting that the customer controls:
   - **Off** (the default): the databases are suspended at the allowance, as on
     Free.
   - **On**: the databases keep running past the allowance until the account
     reaches **twice** its allowance, then they are suspended.
   - Free accounts cannot turn it on. The request is refused with a 403 that
     names the plan and the allowance.
   - Compute above the allowance is not billed until managed PostgreSQL has
     invoicing. When invoicing exists, a separate ADR prices it (the COGS
     report's figure is $0.20 per CU-h).

3. **A cap never deletes anything.** A suspended database keeps its data, its
   restore history, its bindings and its credentials. The suspension is lifted
   by the next UTC month, by an upgrade, or by turning overage on. Nothing in
   this ADR deletes or shrinks a database on any plan. Account deletion stays as
   ADR-20261024150500 describes it.

4. **The account's compute ceiling.** For each account and UTC month, the
   ceiling `C` is:
   - Free, or paid with overage off: the plan allowance `A`.
   - Paid with overage on: `2 × A`.
   - Either way, capped by the operator's `usage.max_monthly_compute_unit_seconds`
     when that is lower.

5. **apid applies the ceiling through Neon's quota.** apid already owns every
   `managed_postgres_*` row and every provider call, so the enforcer runs
   there. After each usage sweep it does this for every account that has a
   database:
   - It reads `u_i`, each live database's own compute counter for the current
     period, from the provider. These counters have no ledger lag.
   - It works out the account's use this month:
     `U = Σ u_i + D`, where `D` is the ledger's usage this month for the
     account's databases that no longer exist. Without `D`, deleting and
     recreating a database would reset its counter.
   - What remains is `R = max(0, C − U)`.
   - It sets each database's quota to `u_i + R`.
   - Neon then stops any one database the moment it alone uses up what remains.
     Between two sweeps, several busy databases can together overshoot by at
     most their combined maximum size times the sweep interval. The next sweep
     shrinks `R`, so the overshoot never compounds.
   - When `R = 0`, every quota becomes `u_i`, so Neon suspends every database.
     apid records the suspension (decision 8).
   - When `C` rises (an upgrade, overage turned on, a new month), the next sweep
     raises the quotas. An upgrade or an overage change also starts a sweep for
     that account right away, rather than waiting for the timer.
   - A database already above the new quota is suspended. One that falls under
     a raised quota wakes on its next connection, as after a normal
     scale-to-zero.

6. **Quotas outlive apid.** A quota stays on the Neon project until apid
   changes it. A create or restore starts a sweep for its account, so a new
   database has a quota within one sweep. If apid stops, each database keeps
   its last quota, `u_i + R`, which is never above `C`. Neon still resets the
   counters on the 1st, so that holds into the next month too. The worst case
   is an account with `n` databases while enforcement is down, bounded by
   `n × C`. The existing stale-ledger alert and a new
   `FaasManagedPostgresCapEnforcementStale` alert cover that case.

7. **The month boundary.** Neon resets project counters on the first day of the
   month, and the ledger already rolls over at the UTC month start. On the first
   sweep of a new month, `U = 0` and `R = C`, so suspended databases get room
   again. The first PR checks that Neon's reset falls on the UTC month start in
   staging. If it does not, the enforcer uses Neon's
   `consumption_period_start` for `u_i` and keeps `U` on the UTC month.

8. **The customer can see it.** The usage response
   (`GET /v1/account/managed-postgres-usage`, `gregale postgres usage`,
   `/dashboard/postgres`) gains these fields, all `omitempty`:
   - `compute_allowance_unit_seconds`
   - `compute_ceiling_unit_seconds`
   - `compute_cap_state`: one of these values:
     - `healthy`.
     - `warning`: at or above 80% of the allowance.
     - `overage`: past the allowance, with overage on.
     - `suspended`: at the ceiling. It is reported only while enforcement
       is `on`, so shadow mode never tells a customer that a running database
       is stopped.
   - `overage_enabled`, which arrives with the overage setting (decision 11).
   - `suspended_at`, when the cap suspended the account's databases. It
     arrives with the warning emails, which persist the per-account cap state.

   `guardrail_state` keeps its existing values. Released SDKs validate it
   against a closed set, so new values would break them. Old SDKs ignore an
   unknown field.

   The usage-warning mail sends one email per account, month and threshold:
   80% of the allowance, the allowance itself (which says whether the databases
   were suspended or are now in overage), and 80% of the ceiling when overage
   is on.

9. **The storage pool blocks writes instead of suspending.** ADR-1430 pools
   storage per account, but today each database may grow to the whole pool, so
   an account with `n` databases can store `n ×` the pool. The enforcer uses
   the same rule for storage:
   - For each database `i` with current logical size `s_i`, and the account's
     total size `S`, it sets `logical_size_bytes = s_i + max(0, P − S)`, where
     `P` is the pool.
   - At the pool, writes that would grow a database fail with Neon's size-limit
     error. Reads, deletes and `VACUUM` keep working, and nothing is suspended.
   - The overage setting applies the same way: with overage on, `P` is twice the
     plan pool.
   - The monthly storage byte-seconds ceiling keeps gating creates, as
     ADR-1430 says.
   - This ships after compute. Today a database's observed spec carries Neon's
     `logical_size_bytes`, and the health collector, resizes and data resources
     all compare the observed spec with the stored one. A storage cap would
     read as `spec_mismatch` everywhere. First, the adapter must report the
     plan limit as the spec and the enforced limit separately.

10. **Shadow before enforce.** `FAAS_MANAGED_POSTGRES_CAP_ENFORCEMENT` on apid:
    - `shadow` (the default for the first release): computes every quota, logs
      it and exports it as metrics, but sends nothing to the provider.
    - `on`: applies the quotas.
    - `off`: does neither.

    Enforcement requires the usage ledger. With `usage.enabled=false`, apid
    refuses to start with `on`. Before any environment runs `on`, staging
    qualification must show all of these on a disposable project:
    - The project's `compute_time_seconds` counter and quota count the same
      compute-unit seconds as the consumption API's `compute_unit_seconds`,
      which the ledger uses.
    - A quota at or below the counter suspends the project's computes at once.
    - Raising the quota lets the next connection wake the compute.
    - A `PATCH` of `settings.quota.compute_time_seconds` leaves
      `logical_size_bytes` as it was. The adapter always sends both, read first,
      so this holds either way.
    - The counters reset at the UTC month start (decision 7).

11. **The overage setting is a sub-feature capability.** It is
    `postgres-overage`, a child of `managed-postgres`, at maturity `internal`.
    Under ADR-20261023115741 it stays hidden until an operator releases it:
    - The routes are `GET` and `PUT /v1/postgres/overage`.
    - The CLI command is `gregale postgres overage [on|off]`.
    - The dashboard shows a toggle on `/dashboard/postgres`.

    An account without the capability reads as overage off. That matches the
    default, so the hidden state changes nothing.

## Delivery

Each step is its own PR, in this order:

1. This record.
2. Neon quota support, behind an optional `ComputeCapProvider` interface, plus
   the enforcer and its apid wiring in `shadow` mode.
3. The customer view, in three PRs:
   - the usage fields and `compute_cap_state`;
   - the warning emails and `suspended_at`;
   - the `FaasManagedPostgresCapEnforcementStale` alert.
4. The `postgres-overage` capability: migration, API, CLI and dashboard, at
   maturity `internal`.
5. The staging qualification probe for decision 10, then an operator step:
   `on` in staging, then in production, after a shadow week whose logged
   quotas match the ledger.
6. The storage pool (decision 9).

## Release compatibility

- The migration is expand-only: a new `managed_postgres_account_overage` table
  written only by apid, and a `suspended_at` column on the per-account usage
  state. It runs before the control plane, as usual.
- An old apid never sets a compute quota, so an old apid next to a new one
  enforces nothing. When the new apid sweeps, it rewrites every quota.
- The quotas live on the Neon project, not in our schema. A rollback, or
  switching enforcement to `off`, leaves the last applied quotas in place. They
  move again only when an enforcing apid sweeps. So neither can silently lift a
  suspension or suspend anyone new.

## Consequences

- No account can run managed PostgreSQL compute past twice its plan allowance,
  and Free and default paid accounts cannot pass their allowance. This closes
  the COGS gap the GA review found.
- A paid customer with a busy database and overage left off is suspended
  mid-month. The 80% email, the dashboard state and the upgrade path have to be
  clear enough that this is never a surprise.
- Turning overage on gives away up to one extra allowance of compute each
  month, unbilled, until invoicing exists. Releasing `postgres-overage` before
  invoicing is an operator decision with that cost.
- Every sweep makes one provider write per database whose quota changed. The
  enforcer writes only when the quota moves by more than one sweep's worth of
  the database's minimum size. This keeps it within Neon's API budget, which
  ADR-492's cooldown already respects.

## Not covered

- The price of overage and its invoicing.
- A per-database cap, or per-database priority within an account's pool.
- Native (non-Neon) providers. A provider without a compute quota is skipped
  and counted as `unsupported`; its databases are capped only by admission.
