# ADR: Managed PostgreSQL production provisioning

Date: 2026-10-28
Status: Accepted
Related: [ADR-155](155-provider-neutral-managed-postgres.md) (staging qualification and canary), [ADR 20261023115741](20261023115741-every-capability-behind-a-platform-flag.md) (capability flags), [ADR 20261027123814](20261027123814-data-api-capability-tree.md) (the Data API sits under managed PostgreSQL)

## Context

Customer provisioning opens only when the daemon runs with
`FAAS_ENVIRONMENT=staging`. It also needs a signed provider qualification
artifact that names a canary account list and expires within 90 days. No
staging environment exists. Production is therefore closed by construction:
neither managed PostgreSQL nor the Data API built on it can reach a customer.
The one production canary (2026-10-14) ran on a hotfix build for about three
hours.

The staging design also does not fit production operations:

- Widening the canary list means a new qualification run and an apid restart.
- An expired artifact closes provisioning with no signal.
- Nothing bounds what all released accounts together can cost the platform at
  the provider.

## Decision

1. **Production approval: a backend pin, not a qualification artifact.** In
   `FAAS_ENVIRONMENT=production` the gate opens only while all of these hold:
   - provisioning is switched on in the configuration;
   - provider usage metering (`usage.enabled`) is on;
   - both fleet ceilings (point 4) are set;
   - there is exactly one default backend;
   - an operator approval pins that backend:
     `FAAS_MANAGED_POSTGRES_PRODUCTION_BACKEND` and
     `FAAS_MANAGED_POSTGRES_PRODUCTION_FINGERPRINT`, approved from
     `…_APPROVED_AT` until `…_UNTIL` (RFC 3339).

   The fingerprint covers the backend's placement configuration, so changing
   it closes provisioning until an operator approves the new one. No provider
   qualification run is required in production; staging keeps its
   qualification gate unchanged.
2. **Approval window.** The window is at most 90 days
   (`ManagedPostgresProductionApprovalMaxWindow`). When it ends, the gate closes
   new databases, bindings, restores, resizes and snapshots. Existing
   databases and credentials keep working, and deletion and revocation stay
   open, as with any closed gate. The approval's end is exported as
   `apid_managed_postgres_production_approval_until_timestamp_seconds`:
   - `FaasManagedPostgresProductionApprovalExpiring` warns 14 days ahead
     (`ManagedPostgresProductionApprovalWarning`);
   - `FaasManagedPostgresProductionApprovalExpired` pages once it has ended.
3. **Accounts by capability flag.** In production the `managed-postgres`
   capability flag decides each account, for its plan and organization: per
   account, plan and org rules, ramps and the kill switch, changed at runtime
   with an audit trail. The staging canary list is ignored in production.
   Workers that only continue work apid already admitted (data pipelines,
   environment clones) do not re-check the flag.
4. **Fleet ceilings.** The configuration's `fleet` block sets
   `max_databases` and `max_monthly_cost_millicents`, bounded by
   `ManagedPostgresFleetDatabasesCeiling` and
   `ManagedPostgresFleetMonthlyCostCeilingMillicents`.
   - **Database cap.** It counts what reservation limits count: undeleted
     databases plus pending clone restores. At the cap, work that adds a
     database (create, restore, snapshot copy target) answers
     `503 managed_postgres_new_databases_paused`.
   - **Spend breaker.** It sums this UTC month's provider cost from the usage
     ledger, one primary-key range per database. At the breaker, any work that
     adds provider cost also pauses: new databases, cost-raising resizes and
     snapshot copy readers. It reopens at the next month.
   - **Precision.** Concurrent reservations can overshoot the cap by the
     number in flight; these are safety ceilings, not quotas. The customer
     message names no fleet sizes or costs.
   - **Alerts.** Fleet totals are exported after every usage sweep.
     `FaasManagedPostgresFleetNearCeiling` warns at 80%, and
     `FaasManagedPostgresFleetPaused` pages at the ceiling.

## Consequences

- An operator can open production provisioning without a staging environment
  or a provider qualification run. The trade is that nothing proves a
  provider's lifecycle, restore and credential behaviour before customers
  depend on it. The fingerprint pin, the capability flag's ramps and the fleet
  ceilings bound the exposure instead.
- Releasing an account is a flag change, not a deploy.
- Production fails closed without metering or fleet ceilings.
- The approval needs renewing at least every 90 days; forgetting pages before
  it closes.
- Multi-region production waits for an approval that pins more than one
  backend.

## Rejected alternatives

- **Production qualification artifact.** It keeps provider evidence, but it
  adds a provider run per renewal, and the artifact's canary list duplicates
  the capability flag.
- **Build a staging environment first.** It is the most rigorous option, but
  it means weeks of infrastructure before any customer gets a database.
- **No expiry in production.** It removes the renewal burden, but provider or
  configuration drift would go unreviewed indefinitely.
- **Plan limits only.** Worst-case provider spend would be every plan ceiling
  times the number of released accounts.
