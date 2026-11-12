# ADR 20261111130000 · Invitations need their inviter's authority; org access, billing and deploy-token changes are mailed

- **Status:** accepted
- **Date:** 2026-11-11
- **Related:** ADR-061, ADR-20261018003100, ADR-20261108150000, ADR-20261110190000

## Problem

The 2026-11-10 account-flow audit found:

1. **Invitations outlived their inviter.** Accepting an invitation checked
   the token, email, expiry and seat cap, never that the inviter could
   still invite. An admin about to be removed could invite a second
   address as admin and, once removed, accept it within 14 days.
2. **Org security events were silent.** Ownership transfers, member
   removals, role changes and admin invitations went only to the in-app
   activity feed. A compromised owner or admin session could reshape an
   organization without anyone being emailed.
3. **Deploy tokens were unnoticed.** Minting or rotating a deploy token (up
   to 365 days) sent no credential notice, and the password-reset key review
   did not list them, although a reset keeps them.
4. **Billing email changes were silent.** Changing an organization's
   billing email moved invoices and billing-portal sign-in links with only
   an audit row.

## Decision

- **Accepting an invitation requires its inviter to be an active owner or
  admin** of the organization (memory and Postgres, with and without the
  activity outbox). Otherwise the invitation is invalid. Invitations with no
  recorded inviter keep their behaviour.
- **Org security mail.** Ownership transfer, member removal, role change and
  an admin invitation mail every owner and the member concerned (masked
  addresses, `OrgSecurityEventBody`). Each event has its own throttle kind
  (`org_ownership_transferred`, `org_member_removed`, `org_role_changed`,
  `org_admin_invited`).
- **Deploy tokens** send the credential notice ("A deploy token" / "A rotated
  deploy token", naming the app) to the member who minted them. The reset
  key review lists the live deploy tokens the account holds
  (`state.DeployTokenHolderLister`).
- **Billing email change** mails the previous billing address and the
  owners (`OrgBillingEmailChangedBody`).

## Consequences

- Owners receive more mail on busy organizations; the per-account security
  mail throttle bounds it per event kind.
- An admin's outstanding invitations stop working when the admin is
  demoted or removed; the owner invites again if they still want them.
- No migration.
