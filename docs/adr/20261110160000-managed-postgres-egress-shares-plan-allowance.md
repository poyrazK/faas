# ADR-20261110160000: Managed PostgreSQL egress shares the plan's egress allowance

- **Status:** accepted (owner, 2026-11-10: "Count it in app egress", on the billing launch review). Slices 1 and 3 built. Decisions 1 and 2 replaced on 2026-11-11 by a per-database allowance that matches Neon (owner: "yeah, lets update our pricing due to this"); see the last amendment.
- **Date:** 2026-11-10
- **Owner:** apid and managed PostgreSQL maintainers
- **Amends:**
  - [ADR-20261025094915](20261025094915-managed-postgres-compute-caps-on-running-databases.md): the cap sweep gains an egress hold next to its compute and inactive holds.
  - [ADR-20261104090000](20261104090000-migrated-egress-build-rollup-egress-ceiling.md) and [ADR-20261104204500](20261104204500-egress-overage-election.md): a plan's monthly egress allowance counts its databases' egress too.

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

- **Database egress had no plan limit.**
  - The managed PostgreSQL usage ledger records each database's egress.
    That covers Neon's data transfer and the bytes pgproxyd counts for
    Gregale's own database nodes.
  - Nothing compared that egress with the plan. The usage policy's
    `max_monthly_egress_bytes` is an operator ceiling, the same for every
    subject. It is checked only when a database is created or restored,
    never while one runs.
  - The app egress allowance (Free 10 GiB, Hobby 100, Pro 500, Scale 2,000)
    counts usage_minutes only.
- **It costs real money.** Production runs on Neon's Scale plan, which bills
  $0.10 per GB past 500 GB across the whole organization
  (ADR-20261102072425). A Free database could read out data without limit.
- **Decision asked of the owner.** The options were: count database egress
  in the existing app allowance, add a separate per-plan database ceiling, or
  keep the operator-wide ceiling only. The owner chose the first.

## Decision

1. **One allowance.** A subject's apps and its databases share the plan's
   monthly egress allowance (`Limits.MonthlyEgressGiB`), counted over the
   same UTC month.
2. **The cap sweep holds the databases.**
   - Each sweep reads the subject's database egress from its usage snapshot
     and its apps' egress from usage_minutes.
   - Once the two together reach the allowance, it holds every database of
     the subject with reason `egress`. That is the same compute hold the
     compute ceiling uses: computes stop and connections are refused.
   - The hold ends when the month rolls over or the plan grows.
   - It applies only in cap enforcement mode `on`; `shadow` logs it.
   - It also binds a subject without a compute ceiling.
   - A failed read fails the subject for that sweep and keeps its previous
     hold.
3. **Egress overage does not lift the databases' ceiling yet.** Database
   egress is not billed, so an account that elected egress overage keeps
   paying for its apps' egress past the allowance, but its databases stop at
   the allowance.

## Amendment 2026-11-10: holds are visible

A held database still reported `ready`, so a customer saw connections fail
with no reason; no email names the egress hold yet.

- `GET /v1/postgres/databases/{id}` and the list report `hold`:
  `compute_ceiling`, `egress`, `inactive`, `plan_excess` or `preview`.
- `GET /v1/postgres/usage` adds `egress_allowance_bytes`, `app_egress_bytes`,
  `egress_state` (healthy, warning at 80%, held) and the account-wide `hold`.
- The read uses the sweep's own rules (`managedpostgres.SubjectHold`,
  `DatabaseHold`, `EgressState`) and the sweep's egress check shares the same
  helper. It runs only while cap enforcement is `on`, so shadow mode reports
  nothing and reads nothing extra. A view can lead or trail the provider by
  one sweep interval.
- The order of the slices below changes: slice 3 (billing database egress)
  lifts the hold only while that charge is live, because the managed
  PostgreSQL overage path is still `internal`.

## Amendment 2026-11-10: slice 3, database egress overage is billed

The owner chose the price ("Neon + 5%") and the split ("Apps use it first").

- **Price.** Database egress past the allowance costs Neon's data transfer
  price plus the managed PostgreSQL markup: $0.105 per GB (10^9 bytes, Neon's
  unit), `api.ManagedPostgresEgressOverageMillicentsPerGB`.
- **Split.** Apps use the shared allowance first. A month's database charge is
  the database egress past what the apps left of the allowance, at most one
  more allowance (`managedpostgres.EgressOverageCharge`). Apps keep their own
  egress overage line; the two never bill the same byte.
- **Who is billed.** A subject whose egress overage election lifts its apps'
  allowance (`billing.EgressOverageLifted`: election on, plan allows it, the
  provider bills egress live). The overage biller lists every subject with
  databases whose election covered the month, and keeps billing a month that
  already charged database egress after the election ends, as storage does.
  An egress election alone never charges compute or storage past their
  allowance.
- **Delivery.** It is one more term of the managed PostgreSQL cumulative
  overage step (`billed_egress_bytes` on deliveries and pending steps, migration
  `20261110213000417`), sent through the same sink and idempotency key.
- **Hold.** With the election lifted and managed PostgreSQL charges live, the
  sweep and the views hold the databases at twice the allowance instead of at
  it, the same 2x ceiling as compute. `egress_state` reports `overage` past the
  allowance and `warning` at 80% of the 2x hold. In shadow or internal
  delivery the hold stays at the allowance, so an election never makes
  database egress free.
- Decision 3 above no longer holds once managed PostgreSQL charges are live.

## Amendment 2026-11-11: the egress hold is mailed

A held customer saw ready databases refuse connections, and only the API said
why. The sweep now mails the hold once per month:

- **Event.** The first enforced sweep that holds a subject for egress records
  cap event threshold `egress_held` (migration `20261111160000418` widens the
  threshold CHECK). Its amounts are bytes: the shared egress apps and
  databases sent, the plan's allowance, and the hold ceiling. `overage` is
  set when the ceiling is twice the allowance because database egress is
  billed.
- **Email.** It goes through the compute cap email path
  (`FAAS_INTERNAL_MAIL_MANAGED_POSTGRES_CAP_OUTBOX_ENABLED`, which cap
  enforcement `on` already requires), mailed on its own inside the 24-hour
  window and retried by later sweeps. It never ranks among the compute
  thresholds, so it neither hides nor is hidden by a compute email. It names
  the allowance, what was sent, the resume date, and how to resume sooner
  (upgrade, or turn on egress overage).
- **Skew.** An apid of the previous release ranks an unknown threshold first
  when it picks the most severe compute event, and it cannot render this
  email. Until every apid runs this release, a held subject's compute email
  can be skipped. Roll apid out in one step.

## Amendment 2026-11-11: only public transfer is egress

Neon's usage reader summed `public_network_transfer_bytes` and
`private_network_transfer_bytes` into the database egress meter. Private
network transfer (Private Link, VPC) never leaves for the internet, yet it
counted toward the shared allowance, could trip the egress hold, and since
slice 3 would be billed at the public transfer price. The meter now counts
public transfer only; the reader still requests private transfer so a later
COGS report can read it. Readings collected before this change keep what they
recorded. Gregale apps reach Neon over its public endpoints, so a database's
replies to an app are still public transfer and still egress; that is a
separate open item.

## Amendment 2026-11-11: each database has its own allowance, as on Neon

The owner asked to check Neon's egress pricing (neon.com/pricing,
docs/introduction/plans and network-transfer, read 2026-11-11). It differs
from what Context above says:

- Neon's paid plans include **500 GB of public network transfer per project**
  a month, then $0.10 per GB. "Public network transfer is counted per
  project, not per account." Context's "past 500 GB across the whole
  organization" was wrong.
- Every Gregale database is its own Neon project (ADR-20261102072425), so
  every database brings its own 500 GB that Gregale already pays for.
  Counting database egress against the plan's app allowance (Free 10 GiB,
  Hobby 100) held databases long before they cost Gregale anything.

The owner chose to match Neon:

1. **Per-database allowance.** Each managed PostgreSQL database has a monthly
   egress allowance of 500 GB (10^9 bytes),
   `api.ManagedPostgresDatabaseEgressAllowanceBytes`, on every plan that
   includes databases. Database egress no longer counts toward the plan's
   app egress allowance, and app egress no longer counts toward a
   database's. Replaces decision 1.
2. **The sweep holds one database.** Each sweep reads every database's egress
   this month (`ManagedPostgresEgressByDatabase`, same waivers as the usage
   snapshot). A database that reached its allowance is held with reason
   `egress`; the subject's other databases keep running. Compute, inactive
   and plan-excess holds keep their precedence. Replaces decision 2.
3. **Overage.** With the subject's egress overage election lifted and
   managed PostgreSQL charges live, a database is held at twice its
   allowance, and each database's egress past its own allowance, at most one
   more allowance, is billed at $0.105 per GB. A month's charge is the sum
   over its databases (`managedpostgres.EgressOverageCharge`). Apps use their
   own allowance and overage line; no byte is in both. `app_egress_bytes` on
   the delivery step stays and is always zero.
4. **Views.** `egress_allowance_bytes` is the per-database allowance and
   `egress_state` is that of the database that sent the most. The
   account-wide `hold` no longer reports `egress`; each held database does.
   `app_egress_bytes` is deprecated and always omitted.
5. **Email.** The `egress_held` event and email stay once per subject per
   month, sent for the first database held. Its amounts are that database's
   egress, its allowance and its hold ceiling, and it says the apps and other
   databases are unaffected.

A database's 500 GB is the same COGS Neon gives Gregale for that project, so
the allowance costs nothing beyond Neon's plan fee; past it, Neon charges
$0.10 per GB and Gregale bills $0.105. Without an elected overage a database
stops at its allowance and never costs extra.

## Later slices

- **Slice 2:** dropped by the per-database amendment: apps and databases no
  longer share an allowance.
- **Slice 3:** built, see the amendments above.
- **Slice 4:** built, see the amendment below.

## Amendment 2026-11-12: slice 4, a provider egress quota backstop

The sweep holds a database that sent its egress allowance. Since database
egress is billed, a sweep that stops running would let a database keep
sending, and Neon bill Gregale for it. Neon enforces its own per-project
quota, `settings.quota.data_transfer_bytes`: once the project's
`data_transfer_bytes` counter for the consumption period reaches it, Neon
suspends every compute in the project until the quota is raised or the
period ends.

- **Target.** Each sweep sets a provider object's quota to the hold ceiling
  (the allowance, or twice it while overage is billed live) times the
  databases sharing that object. A restore is a branch in its source's
  project, so a project with a restore gets two allowances, as the two
  databases each have one.
- **Backstop only.** The quota is written after the holds and never skips
  one; a failed write fails the subject for that sweep. A provider without
  the capability (`managedpostgres.EgressCapProvider`), or one whose period
  does not start within an hour of the UTC month, is skipped. A subject
  without an allowance keeps its quota as it is, because zero means no
  quota to Neon.
- **Writes keep each other.** The compute, storage and transfer quota writes
  each send the project's other quotas as read and confirm all three by
  reading the project again.
- **Mode.** It follows cap enforcement: `shadow` logs the decision, `on`
  writes it. `apid_managed_postgres_egress_quota_decisions_total{outcome}`
  counts `applied`, `shadowed`, `unchanged` and `failed`.
- **Owed.** A native Neon check that `data_transfer_bytes` counts public
  transfer only, as the usage meter does since the 2026-11-11 amendment; if
  it also counts private transfer, a project using Private Link reaches the
  quota before the sweep's hold.

## Amendment 2026-11-13: one hold email per database

Since databases are held one at a time (amendment 2026-11-11), the
subject-level `egress_held` event mailed only the first database held in a
month, and the email could not say which one. The hold is now recorded and
mailed per database:

- **Notice.** `managed_postgres_egress_hold_notices` (migration
  `20261113170412214`) holds one row per database and UTC month: the egress
  the database had sent, its allowance and the hold ceiling in bytes,
  whether overage is billed, and the email's intent. The first enforced
  sweep that holds a database records it; later sweeps leave it.
- **Email.** Each notice is mailed once through the same
  `managed_postgres_cap` path and 24-hour window, linked to its row instead
  of the subject's event. The subject line and body name the database
  ("Managed PostgreSQL database orders is paused until next month"); a
  second database held later in the month gets its own email.
- **Skew.** The sweep no longer writes subject `egress_held` events; rows
  already written stay and are never read again. While an apid of the
  previous release runs beside this one, a held database can be mailed by
  both, once each. The sealed email payload gains an optional
  `database_name`; the previous release cannot open a payload that carries
  it, so roll apid out in one step, as for the 2026-11-11 email.

## Consequences

- Before 2026-11-11, a Free account's databases stopped after 10 GiB of
  combined egress a month. Since then each database stops at its own
  500 GB; the sweep interval bounds the overshoot.
- Egress counts and stops are visible as `managed postgres compute held`
  logs with reason `egress`, and the owner is mailed once per held database
  and month (amendments 2026-11-11 and 2026-11-13).
- Rejected: a separate database ceiling (two allowances to explain, and it
  doubles what a plan gives away), and the operator ceiling alone (not
  plan-aware, and never checked while a database runs).
