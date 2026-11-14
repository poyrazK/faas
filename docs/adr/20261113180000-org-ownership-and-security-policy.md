# ADR-20261113180000 · Organization ownership guards and a require-2FA policy

- **Status:** accepted
- **Date:** 2026-11-13
- **Amends:** ADR-202610171901 (organization roles), ADR-20261110100000 (session-only proof), ADR-20261111130000 (organization security notices)
- **Context:** The 2026-11-10 account-flow audit, passes 9–14, found these organization gaps:
  - Ownership could be transferred to a suspended account or one pending deletion. The organization was then left with an owner who cannot act, and support had no tool to fix it.
  - A full-access API key could transfer ownership or delete the organization. A leaked key was enough for either irreversible step.
  - An owner could not require members to use two-factor authentication.
  - An admin could invite another admin, giving themselves a second admin identity that the owners never approved.
  - An admin's new or rotated organization key reached the owners only through the activity log.
- **Decision:**
  1. A database trigger refuses any owner membership whose account is not `active` or `past_due` (constraint `org_owner_account_inactive`). Both stores map it to `ErrOrgOwnerInactive`, and the API answers 409. An existing trigger already stops deleting the owner of a shared organization; it is unchanged.
  2. Transferring ownership and deleting an organization need a signed-in browser session. Deletion also needs a fresh step-up and recent presence. An API key gets 403 `browser_session_required`. Deleting your own account is unchanged.
  3. `org_security_policies.require_mfa`: while it is on, a member without two-factor authentication is refused on the organization's routes with 403 `org_mfa_required`, by session and by API key, until they enrol. The rule sits in the shared membership snapshot and lock queries, so every org-scoped access path and fence applies it, and `loadOrg` returns the clear error.
     - Only an owner can change the policy (`org.manage_security`), with a fresh step-up.
     - Turning it on needs the caller to have two-factor authentication.
     - A change is audited and mailed to owners and admins.
     - Personal API keys are not restricted.
  4. Only an owner can invite an admin.
  5. Creating or rotating an organization API key mails the owners (`org_api_key_created`). Deleting an organization mails owners and admins (`org_deleted`).
  6. An operator can make an active member the owner when the current owner cannot act: `POST /v1/admin/ops/accounts/{id}/make-org-owner?org=<slug>` (`gregalectl accounts make-org-owner --org`). The previous owner becomes an admin. The action is audited and mailed.
- **Consequences:**
  - An organization always has an owner who can sign in, and support can repair one that does not.
  - Automation that deleted organizations or transferred ownership with keys must use the dashboard.
  - Turning on require-2FA immediately locks out members without it, keys included. The notice email tells owners and admins.
  - Release skew: the trigger and table are expand-only. An older apid ignores the policy until it is upgraded.
