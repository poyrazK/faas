# ADR-20261103131742: Billing follows the organization's email, plans bind after a downgrade, and unbilled egress has a ceiling

- **Status:** accepted (owner, 2026-11-03: "Lets solve those issues in the next pr", on the round 6 billing audit)
- **Date:** 2026-11-03
- **Owner:** apid (billing), schedd and meterd maintainers
- **Amends:**
  - [ADR-20261030093512](20261030093512-plan-time-allowance-and-organization-spend-caps.md): decision 4 ("Not changed: limits after a downgrade") and the rejected alternative of turning off Polar's portal plan switching.
- **Extends:**
  - [ADR-20261102125208](20261102125208-managed-postgres-databases-past-a-lowered-plan-are-suspended.md): apps past a lowered plan are handled the same way as its databases, oldest first.

## Context

1. **Polar keeps one customer per email.** Sandbox, 2026-11-03: creating a
   second customer with an email in use answers 422 "A customer with this
   email address already exists."; deleting the customer (204) frees the
   email; `external_id` cannot be changed. An organization's first checkout
   bills the acting member's email (`orgbilling.BillingContact`). So:
   - an owner who already pays for a personal plan can never start the
     organization's checkout;
   - a returning customer whose account was deleted can never pay again,
     because the deleted account's customer keeps the email;
   - the portal signs in by email one-time code, so a member removed from the
     organization keeps signing in to its billing portal, where they can cancel
     the subscription and read invoices. Nothing could move the email.
2. **Plan entitlements outlive the plan.** Deployed-app count and per-app RAM
   are checked only when an app is written. `planDowngradeProblem` (H17-4)
   refuses an API downgrade that does not fit. A subscription that ends or is
   revoked, or a provider-side change, lowers the plan with no check. Polar's
   portal can also switch plans (`update_plan`). With
   `proration_behavior=invoice`, a Hobby customer could switch to Scale for
   minutes, create 100 apps of 2 GB, and switch back for most of the difference.
   All 100 apps kept serving. On Free that is bounded only by the GB-hour
   allowance. A later Hobby subscription then had 100 apps where Hobby
   allows 5.
3. **Application egress is neither billed nor capped.** The Polar egress meter
   is off by default (`EgressBillingMode=off`). Its included allowances exist
   only as optional environment values. Free has no egress policy at all. The
   only bound is the per-instance tc rate (Free 10 Mbit … Scale 250 Mbit).

## Decision

1. **The organization's billing email is its own, and the customer moves with
   it.**
   - `PUT /v1/orgs/{slug}/billing/email` sets it. It is gated like a plan
     change: owner, admin scope, verified email, MFA, step-up and
     `Idempotency-Key`.
   - When the organization already has a provider customer, apid moves that
     customer first, then saves the email. `PATCH /v1/customers/{id}`
     (`billing.CustomerEmailUpdater`).
   - Errors:
     - an email another customer holds answers 409 `billing_email_in_use`;
     - any other provider error answers 502 `billing_email_change_failed` and
       leaves both sides unchanged.
   - Removing the member whose email is the billing email answers 409
     `billing_contact_member` while the organization has a provider customer,
     and so does that member deleting their own account.
2. **A checkout that meets the email in use answers 409, or reclaims a
   deleted subject's customer.**
   - `CreateCustomer` reports the customer holding the email
     (`billing.CustomerEmailInUseError`, found with
     `GET /v1/customers/?email=`).
   - When that customer's `external_id` names no live account or organization,
     and no billing identity points at it, it is a deleted subject's leftover.
     apid deletes it (`billing.CustomerDeleter`, audited as
     `billing.leftover_customer_deleted`) and creates the new customer.
   - Otherwise the plan change answers 409 `billing_email_in_use`, and the
     dashboard shows a banner.
   - Polar ignores `Idempotency-Key` (sandbox, 2026-11-03: the same key created
     a new customer after the first was deleted), so the retry is a real
     create.
3. **The customer portal does not switch plans; existing subscribers upgrade
   through the API.**
   - An upgrade of an existing subscription sends
     `proration_behavior=invoice`. Polar charges the prorated difference now
     and applies the change only if the charge succeeds.
   - The upgrade request is sent once, without the client's transport
     retries, because Polar does not honour `Idempotency-Key` (decision 2). A
     customer's retry after a declined card is a new charge.
   - Responses:
     - an accepted upgrade answers 202 `pending_provider_confirmation`, and the
       webhook still moves the local plan;
     - a declined charge answers 402 with the billing portal URL to update the
       card;
     - a downgrade keeps `next_period`.
   - The dashboard upgrade page offers "Upgrade now" to subscribers.
   - Billing does not start (`EnsurePlanProducts` fails) while the Polar
     organization has `customer_portal_settings.subscription.update_plan` on
     or missing.
4. **Apps outside the subject's plan park and do not wake.**
   - The subject's deployed apps are those in state active or evicted_cold,
     excluding developer and PR preview apps. They are counted as in the quota
     (`state.PlanSubjectApps`) and ranked oldest first.
   - An app is outside the plan if it ranks past `DeployedApps`, or if its
     RAM is above the plan's `RAMMB` (`state.AppsOverPlan`).
   - schedd refuses to wake an app outside the plan: 403 `plan_limit_apps` or
     `plan_limit_ram` with `ErrPermanentWake`. This covers requests and
     explicit deployments. Deployment smoke and mirrors are exempt, as for the
     spend cap.
   - The park sweep (`appMustPark`) parks it.
   - The answer is cached per subject for a minute. A plan change recomputes
     it at once.
   - A read error fails open.
   - Deleting apps, lowering RAM or upgrading lets the apps wake again.
5. **Unbilled egress stops at the plan's monthly egress.**
   - `api.Limits.MonthlyEgressGiB` is Free 10, Hobby 100, Pro 500, Scale 2,000.
     These match each plan's object storage egress.
   - meterd sums the month's `NetTxBytes` (the egress meter's source). While
     the provider does not bill egress live for the plan, reaching the
     allowance stops the subject like Free's compute stop:
     - the quota suspension (`suspended_reason='free_quota'`);
     - instances parked as `quota_exceeded_egress`;
     - `billing_past_due` with `"reason":"egress"`.
   - The stop lifts when the month ends or the plan grows. Free is never billed
     for egress, so Free always has the ceiling.
   - Once egress bills live, the allowance is what Polar includes. An unset
     `FAAS_POLAR_*_INCLUDED_EGRESS_GIB` now takes the limits.go value. Usage
     above it is overage under the spend cap, not a stop.
6. **The subscription-ended email says what now happens on Free**: only the
   oldest deployed app serves, an app above 256 MB stops waking, and the egress
   allowance. The outbox stores only template version 1, so version 1's text
   changes once, in place.

## Consequences

- An organization can pay from its own address, and a member's departure
  cannot leave billing control behind. The API and SDKs carry the route
  (`SetOrgBillingEmail`, `setOrgBillingEmail`, `set_org_billing_email`); a
  CLI command and a dashboard form are follow-ups.
- Production go-live needs portal plan switching off in the Polar dashboard
  (Settings, Customer portal) first. The provider refuses to start otherwise.
  The sandbox organization needs the same change before sandbox billing starts.
- A customer whose subscription lapses with more apps than Free allows keeps
  its oldest app serving; the rest stop until it fits or upgrades. This is the
  product change ADR-20261030093512 deferred. Apps created outside the quota
  check would be counted too; none are known.
- Paid subjects can now be stopped by egress until egress billing is turned
  on. Prod traffic is mostly tests (2026-10-29), far below 100 GiB a month.
- Rejected:
  - **Keeping portal plan switching and enforcing limits only.** The portal
    round trip would still cost little for a month of Scale RAM and
    concurrency.
  - **Deleting any customer that holds a wanted email.** A live subject's
    customer is never deleted; only a deleted subject's leftover is.
  - **A spend-cap hold for egress.** Holds apply only to subjects with a cap
    set, and Free has none.
  - **Billing egress live by default.** Pricing egress is a catalog decision
    for the owner; the ceiling bounds cost until then.

## Amendment 1 (2026-11-04): scheduled changes can be seen and undone

Billing audit round 7 found that a scheduled cancellation or downgrade could
be neither seen nor undone through Gregale. Polar refuses every product change
on a subscription whose `cancel_at_period_end` is set (403
`AlreadyCanceledSubscription`), and apid answered that refusal with 402 "update
the payment method". A request for the plan already held changed nothing and
answered 200. The restore notice after a cancelled deletion pointed at a
checkout that is not offered while a subscription exists. Only Polar's customer
portal could undo the change, and nothing pointed there. The customer dropped
to Free at the period end, and decision 3 then parked every app above Free.

Polar also refuses a second cancellation with the same 403. apid treated it as
a provider failure: `POST /v1/billing/cancel` answered 502, and deleting an
account whose subscription was already cancelled failed until the period ended.

Decisions:

1. **Asking for the current plan keeps the subscription.** `PATCH
   /v1/account/plan` (and the organization route) with the plan the subject
   already pays for undoes a scheduled cancellation (`cancel_at_period_end:
   false`) and a scheduled plan change (`pending_update: null`). It answers 200
   with `plan_change_status: scheduled_change_undone` when something was
   undone. Neither request moves money. The dashboard offers the same thing as
   Keep subscription, and the account plan selector schedules downgrades
   through the provider rather than sending the customer to a portal that no
   longer switches plans.
2. **Provider refusals have their own answers.** A plan change refused because
   the subscription is ending answers 409 `billing_subscription_ending`, naming
   the request that keeps it. An upgrade whose charge the bank asks to confirm
   (Polar `PaymentActionRequired`) answers 402 `payment_authentication_required`
   with the portal link, where adding the card again lets the bank confirm it.
   A declined charge (`PaymentFailed`) keeps the 402 `payment_required` answer,
   now saying the card was declined. A second cancellation is
   `ErrAlreadyCancelled`. A downgrade to Free on a subscription already ending
   returns its end date, and deletion skips a subscription that is already
   ending.
3. **Scheduled changes are visible.** `GET /v1/billing/status` reports
   `subscription_ends_at`, `scheduled_plan` and `scheduled_plan_at`, read from
   the provider within the webhook path's 5-second bound and left out when the
   read fails. The dashboard billing and upgrade pages show the same change.
4. **The portal link and the spend cap take what cancelling takes.**
   `GET /v1/billing/portal` returns a signed-in provider session that can
   cancel and replace the card, so it now requires an admin credential, MFA, a
   verified email and, for a session, a step-up within 5 minutes; a `usage:read`
   key used to get it. `POST /v1/account/overage-cap` adds the step-up the
   dashboard and organization cap routes already had. Suspended accounts still
   reach the portal to pay.
5. **Prices are shown before tax.** Polar adds sales tax or VAT on top as
   merchant of record; the plans page, the dashboard pricing, billing and
   upgrade pages, and the billing docs say so.

Not done: restoring a deleted account does not undo the deletion's
cancellation by itself, because the customer may have cancelled before asking
for deletion; the restore notice names Keep subscription instead. Whether a
re-added card passes a later upgrade depends on the bank; it still needs a
sandbox run with a 3-D Secure test card.

## Amendment 2 (2026-11-04, revised 2026-11-08): the GA charging fixes

- **Owner decisions (2026-11-04, billing GA questions):** egress "stop free,
  on paid plans default no overage at egress, if overage enabled, 0.03 on
  gib"; builds "Free, monthly quota"; the go-live gate includes fixing the
  charging bugs first.
- **Revision (2026-11-08):** the egress overage election landed separately
  (ADR-20261104204500, #6743) and the monthly build-minute quota has its own
  decision (ADR-20261104132753). This amendment keeps only what neither
  covers: the egress price, per-hour plan pricing and the charging fixes
  below. An earlier draft's `egress_overage_choices` table and build quota
  were dropped in favour of those.

The billing GA review found these charging bugs:

- meterd priced each egress hour by the subject's current plan. A downgrade
  re-priced earlier hours, and hours spent on Free could be billed after an
  upgrade.
- The egress price came only from an environment value.
- A `min_instances` floor was priced without its sidecars. The sampler kept
  writing floor rows for subjects whose spend cap had stopped them, so they
  were billed for time that did not run.
- The object storage allowance was the largest plan held in the month: a
  one-day upgrade gave the whole month the bigger allowance.
- Managed PostgreSQL overage was billed for the whole month against the
  current plan. A downgrade re-billed usage the larger allowance had already
  covered, and an upgrade dropped overage that had already been billed.

Decisions:

1. **Egress overage costs $0.03 per GiB.** `api.EgressOverageMillicentsPerGiB`
   = 3,000 is the published rate. The Polar provider defaults to it and
   refuses to start with any other egress price while egress billing is
   shadow or live. Who pays it is ADR-20261104204500's election.
2. **Each egress hour is priced by the plan held in that hour.**
   - The plan comes from the subject's price snapshots (`monthGrants.planAt`).
     A Free hour settles with a zero receipt, also after an upgrade.
   - The allowance and price the hour is billed against are that plan's.
3. **`min_instances` floors are priced and stopped like the instances they
   stand for.**
   - A floor row is priced at
     `api.BillableRAMMBWithSidecars(app.RAMMB, sidecars)` of the floor's
     deployment.
   - The sampler writes no floor row for a subject whose cap is reached,
     using schedd's admission rule: compute overage at or above the cap, or a
     hold that applies to this cap.
   - A failed cap read writes the row.
4. **The object storage allowance is shared across the month's plans.** It
   is weighted by how long each plan was held
   (`state.MonthPlanSpans`, `state.WeightedMonthAllowance`), like the
   included GB-RAM-hours.
5. **Managed PostgreSQL overage is billed per plan span.**
   - Each span's usage above that span's plan allowance is billed, as app
     compute is billed hour by hour against the allowance in force
     (ADR-20261030093512). Closed months keep their recorded terms.
   - The month's charge is then clamped once to the current plan's ceiling:
     one more allowance.
   - Suspension stays on the current plan's limits. After a downgrade, the
     month's usage so far counts against the new allowance. Suspension
     protects capacity, not the bill.

Consequences:

- $0.03 per GiB is below the list price of internet egress on the current
  GCP production (about $0.085 to $0.12 per GiB). Elected egress overage
  loses money there until production moves to bare metal with bundled
  traffic, or the price changes.
- A deployment whose Polar egress billing is shadow or live with
  `FAAS_POLAR_EGRESS_MILLICENTS_PER_GIB` set to anything but 3000 fails to
  start its billing provider: unset it or set 3000 before upgrading.
