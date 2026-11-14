# ADR 20261110190500 · Organization credentials act as the member who holds them

- **Status:** accepted
- **Date:** 2026-11-10
- **Related:** ADR-061, ADR-20261018003100 (organization access revocation), ADR-20261026151103, ADR-20261104085712, ADR-20261109120000

## Problem

The 2026-11-10 account-flow audit found three ways an organization
credential acted as someone other than the member holding it, or reached
beyond the organization it was issued for:

1. **Rotating someone else's organization key.** `POST
   /v1/orgs/{slug}/keys/{id}/rotate` was allowed to owners and admins for
   any key in the organization. The new key kept the old key's holder,
   scopes and expiry (admin keys never expire), and the plaintext went to
   the caller. An admin could rotate the owner's key and act as the owner,
   for example to transfer ownership to themselves. The owner's old key
   kept working through the grace window, and the credential notice went
   to the admin.
2. **Deploy tokens on company apps acted as the app's creator.** App routes
   hand handlers the app's resource account, whose id is the app creator,
   and the token was stored under that account. A token an admin minted on
   a teammate's app authenticated as the teammate, so its membership check
   and role were the teammate's. It survived the admin's removal, and the
   audit row named the creator rather than the admin.
3. **Organization keys reached the holder's personal account.** Keys
   limited to projects and keys used on personal apps were fenced, but a key
   bound to a company organization could call `POST /v1/keys` (minting an
   unbound personal admin key), export the personal account, or delete it.
   A leaked company CI key, or a key obtained through point 1, became the
   person's whole personal account.

## Decision

- **Only the holder rotates an organization key.** The rotation loads the
  key and answers `403` when its holder is not the caller, recording
  `api_key.rotate_refused`. Owners and admins still revoke any member's key.
- **Deploy tokens record their minter.** `deploy_tokens.created_by_account_id`
  (nullable, `ON DELETE CASCADE`) is the member who minted or last rotated
  the token, passed to the store with `state.WithDeployTokenMinter`.
  `account_id` stays the app's creator, so listing and revocation are
  unchanged. A token whose minter differs from `account_id` authenticates
  as the minter, bound to the app's organization, so access checks use the
  minter's current membership and role. The organization-access fence
  matches `coalesce(created_by_account_id, account_id)`. The membership
  trigger requires the minter to be an active member, and removing a member
  revokes the tokens they minted as well as those on their apps. Audit rows
  name the acting member and record the app's account separately.
- **Organization keys cannot manage the holder's personal identity.** A key
  bound to an organization that is not the holder's personal organization
  is refused (`403`, audited as `key.org_key_personal_route_refused`) on
  `/v1/keys`, `/v1/auth/sessions`, and the account routes for deletion,
  profile changes, restore, export, email, MFA, sign-in methods, security
  activity, billing and plan. `GET /v1/account` stays open. Keys minted with
  `POST /v1/keys` are bound to the personal organization and are unaffected.

## Consequences

- Deploy tokens minted before this change have no minter and keep acting as
  the app's creator. They expire within a year; owners can revoke them.
- A deploy token minted by an admin stops working when that admin leaves,
  even though the app's creator remains a member.
- Automation that rotated another member's organization key must have that
  member rotate it, or revoke it and mint its own.
- Release N-1 running beside N ignores the minter column: during the
  rollout a new token can still authenticate as the app's creator on an
  N-1 apid.
- Migration `20261110163529554_deploy_token_minter` adds the column,
  replaces the two trigger functions, and recreates the membership trigger
  to watch the new column.
