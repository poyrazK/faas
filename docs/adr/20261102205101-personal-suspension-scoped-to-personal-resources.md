# ADR 20261102205101 · A personal billing suspension blocks personal resources only

- **Status:** proposed
- **Date:** 2026-11-02
- **Amends:** the auth middleware's inactive-account refusal
  (`pkg/auth/middleware`, `InactiveAccountMayReach`) and application
  authorization (`pkg/apid/appaccess`) for organization members.
- **Related:** bug hunt 2026-10-28 H87; H50 (strangers can exhaust a Free
  allowance)

This record has a date-and-time identifier because numbered ADRs collide
across parallel branches.

## Context

The auth middleware refused every route with 402 "Account suspended" when the
caller's own account was not active, apart from the billing-recovery routes.
Organization members always act as their personal account, and organization
API keys resolve to the member who created them. A personal account is
suspended when its Free allowance runs out (until the month ends or it
upgrades) and by dunning when a personal subscription stays unpaid.

So a member whose personal Free hobby app used its 20 GB-h, which strangers
can cause by sending it traffic (H50), or whose personal card failed, lost all
access to their company's apps, logs and deploys although the company's plan
and billing were in good standing. Every organization key that member created
stopped at the same moment, so the company's CI failed with 402 too.

Application authorization (`appaccess`) already decides a company app by the
company: the organization's status and the resource owner's
(`state.AppResourceAccount`) activity, and the member's role.

## Decision

1. **The middleware lets a billing-suspended caller reach app-addressed
   routes.** A caller whose account status is `suspended` and who has no abuse
   hold passes the middleware on routes whose pattern addresses an
   application that `appaccess.Wrap` resolves and authorizes before the
   handler runs: `/v1/apps/{slug}...`, `/v1/orgs/{organization}/apps/{slug}...`,
   `/v1/deployments/{id}...` and `/v1/builds/{id}...`. Every other route keeps
   the 402, as before.
2. **Personal apps still refuse.** `appaccess` answers a personal app (no
   organization, or the caller's personal organization) with the same 402
   when its owner, the caller, is not active and the route is not a
   billing-recovery route. A company app is decided by the company as before:
   a suspended company still allows only reads, and an inactive resource
   owner still refuses.
3. **Unchanged:** an abuse hold (ADR-361) and `deleted_pending` lock every
   route; the billing-recovery allowlist is unchanged; deploy admission keeps
   checking the resource owner (`MayDeploy`).

## Consequences

- A member can read, deploy and operate company apps, and organization keys
  they created keep working on company apps, while their personal account is
  stopped for billing. Their personal apps, account routes and app creation
  still answer 402.
- Routes that do not address an app by slug or ID stay refused for a
  suspended member: organization management under `/v1/orgs/{slug}`, projects,
  and resumable uploads (`/v1/uploads`, whose target app is in the body), so
  source deploys by such a member still fail until those routes resolve their
  resource before the account check. Follow-up.
- Organization keys still belong to the member who created them; removing a
  member still stops their organization keys (offboarding). Moving
  organization keys to the organization is a separate decision.

## Rejected alternatives

- **Decide every route by its resource in the middleware.** The middleware
  does not know a route's resource; loading it there duplicates appaccess and
  its sibling wrappers.
- **Exempt organization keys only.** Leaves members' sessions and personal
  keys locked out of company apps, which is most of the reported impact.
