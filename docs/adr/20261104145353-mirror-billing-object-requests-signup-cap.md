# ADR-20261104145353: Mirror VMs bill, object storage has monthly request ceilings, and sign-ups have a platform-wide daily cap

- **Status:** accepted (owner, 2026-11-04: "Lets solve those issues", on the round 6 cost-control audit). Defaults chosen in the owner's absence are marked *assumed*.
- **Date:** 2026-11-04
- **Owner:** meterd, schedd and apid maintainers
- **Amends:**
  - [ADR-124](124-traffic-mirroring.md) and [ADR-133](133-traffic-mirroring-pr-a3-follow-on.md): the sampler's mirror carve-out is removed, so mirror VMs bill as ADR-124 already told customers.
  - [ADR-1162](1162-billing-delivery-finality-spend-cap-and-plan-history.md): mirror admissions stop at the spend cap.
  - [ADR-20261101214152](20261101214152-object-storage-ceiling-follows-spend-cap.md): plans gain monthly request ceilings.
  - Bug hunt H25 (`signupNetworkLimiter`): a platform-wide cap is added next to the per-address and per-network caps.

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

The round 6 cost-control audit found three gaps.

1. **Mirror VMs were never billed.**
   - The meter skipped every `mode='mirror'` instance
     (`state.IsMeteredSkippableMode`), and mirror admissions skipped the
     spend cap because of that.
   - ADR-124's plan error tells customers a mirror wake is "billed per
     running second".
   - Pro allows 1 mirror target per app and Scale 3, each running up to 5
     VMs at once (`MirrorMaxConcurrentPerRule`). A Pro account with about 12
     source and target pairs at 1 GB could hold about 60 GB of unbilled RAM.
     That is more than one node's 47.6 GB tenant budget, and it ran outside
     the spend cap.
2. **Object storage had no per-plan request ceiling.**
   - Every plan's `MaxMonthlyRequests` was 0, so one operator-wide value
     applied to every plan.
   - Raising the operator caps for GA would give Free the same request
     ceiling as Scale.
   - GCS charges about $5 per million writes. At the 100 requests a second
     each account may make, that is about $1,300 a month for one account,
     and requests are not billed.
3. **Free allowances multiply with new accounts.**
   - One address may create 10 accounts a day and one network 50, and every
     account carries a full Free allowance.
   - With fresh addresses there was no upper bound on what a signup farm
     could claim each day.

## Decision

1. **Mirror VMs bill.** The sampler bills a mirror VM plan RAM + 8 MB per
   running second like every other mode, under `mode="mirror"` in
   `metered_mb_seconds_total`.
   - `state.IsMeteredSkippableMode` is removed.
   - The minimum-instance floor still counts only serving instances, so a
     mirror never lowers the floor's billed rows.
   - Mirror admissions now check the account spend cap like floors,
     services and workers. Deployment smoke stays exempt.
2. **Monthly request ceilings.** `ObjectStoragePlanLimits.MaxMonthlyRequests`
   is set per plan. The operator policy still wins where it is tighter.

   | Plan | Requests each month |
   |---|---:|
   | Free | 50,000 |
   | Hobby | 500,000 |
   | Pro | 2,500,000 |
   | Scale | 10,000,000 |

   - *Assumed* numbers, chosen so that even all writes cost less than the
     plan's price.
   - Requests stay unbilled. At the ceiling, requests are refused until the
     month ends, as the existing request budget already does.
3. **Platform-wide sign-up cap.** `api.SignupAccountsPerDay` (*assumed*:
   2,000) caps the accounts created in any 24 hours, counted from
   `accounts.created_at`.
   - Past it, every sign-up method answers the same 429 as the per-network
     cap and records `auth.signup_fleet_cap`.
   - A failed count admits, because the per-address and per-network caps
     still apply.
   - The count reads `accounts` only on a sign-up attempt. It has no index
     on `created_at`; one is a follow-up if sign-up volume makes the scan
     matter.

## Consequences

- **Mirrors.** Customers with mirror rules (Pro and Scale) now pay for the
  mirror VMs they asked for, and mirrors stop at the spend cap. The plans
  page and ADR-124 already said mirrors bill.
- **Object storage.** The request cost is bounded per plan, independent of
  the operator file. A very request-heavy app on a low plan meets a clear
  monthly limit.
- **Sign-ups.** A launch-day spike beyond 2,000 accounts a day is refused
  until the window rolls. Raise `SignupAccountsPerDay` before a planned
  launch.
- **Not decided here.** Disposable-email blocking and a card or phone check
  before Free compute starts are left to the owner.
