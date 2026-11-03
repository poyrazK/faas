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
