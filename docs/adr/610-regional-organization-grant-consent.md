# ADR-610: Regional organization grant consent

Date: 2026-10-04
Status: Accepted; shared runtime admission and directory attachment pending.

## Context

ADR-609 separates directory organization ownership from the app creator.
Regional release grants still authenticate that creator and admit personal apps.
Sharing a creator credential would bypass current membership and attribute
consent to the wrong account. Regional organization plans and billing require
their own runtime admission protocol before shared releases can execute.

## Decision

Regional grants retain their immutable app account and organization anchors and
add an immutable `issuer_account_id`. Personal grants backfill the same account
and preserve their existing bearer identity. For shared apps, the issuer may be
a different current owner or administrator. Grant registration, inspection and
revocation compose the existing organization credential management action
matrix and normal bearer scopes, MFA and verified-email gates. Sessions,
app-scoped ordinary keys, platform-tenant keys and keys bound to another
organization cannot manage these grants. Request hints cannot switch tenants.

The scoped bearer authenticates its issuer, never an account fabricated from
the organization's billing plan. Authentication rechecks the current app owner
anchors, organization status, issuer account and active owner/admin membership.
Creator departure does not affect another administrator's consent. Losing the
issuer role, removal, account hold or organization suspension denies use.
Another current administrator can revoke the retained grant even when its issuer
is inactive. Revoked state remains terminal after role restoration. Issuer UUIDs
are historical attribution without an account-deletion cascade.

Registration and revocation lock the app before the grant, then lock live
organization, actor and membership state through commit. SQL guards apply the
same authority checks to raw inserts, retain grant identities and enforce the
existing per-app active grant bound. The append-only migration refuses rollback
while shared or distinct-issuer consent remains; personal populated rollback
preserves grants. Runtime SQL uses SQLC.

This adds organization consent management to the regional boundary. Shared
directory binding and regional image release acceptance remain gated until
organization identity proof, global quota allocation and regional billing
admission are integrated. A shared grant alone allocates no runtime capacity.

## Validation

Exercise distinct issuer/creator identity, current role and tenant isolation,
idempotent immutable consent, administrator replacement and revocation, member
departure, account/organization inactivity, raw SQL bypasses, lock-wait authority
changes, parallel quota enforcement, personal migration round trips and shared
downgrade refusal. Authenticated HTTP tests cover key scopes and tenant hints,
purpose isolation, SDK registration and revocation, and continued shared release
admission refusal. Native KVM recovery acceptance remains required for the full
multi-region feature.
