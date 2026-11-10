# ADR-20261110160000: Managed PostgreSQL egress shares the plan's egress allowance

- **Status:** accepted (owner, 2026-11-10: "Count it in app egress", on the billing launch review). Slice 1 built.
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

## Later slices

- **Slice 2:** meterd's app egress stop counts the databases' egress too, so
  apps also stop once databases spent the allowance.
- **Slice 3:** database egress past the allowance is billed with the egress
  overage election, and the election then lifts the database ceiling too.
- **Slice 4:** a Neon per-project `data_transfer_bytes` quota as a backstop,
  like the compute quota.

## Consequences

- A Free account's databases stop after 10 GiB of combined egress a month;
  the sweep interval bounds the overshoot.
- Egress counts and stops are visible as `managed postgres compute held`
  logs with reason `egress`; a customer email is a follow-up.
- Rejected: a separate database ceiling (two allowances to explain, and it
  doubles what a plan gives away), and the operator ceiling alone (not
  plan-aware, and never checked while a database runs).
