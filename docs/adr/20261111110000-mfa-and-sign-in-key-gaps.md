# ADR 20261111110000 · Two-factor changes, CLI approval and sign-in keys need fresh proof

- **Status:** accepted
- **Date:** 2026-11-11
- **Related:** ADR-077, ADR-20261109120000, ADR-20261110100000, ADR-20261111090000

## Problem

The 2026-11-10 account-flow audit found that several account changes
accepted a session or a password where a fresh proof was due, and that
CLI and MCP sign-in keys outlived recoveries that ended every session:

1. **Turning MFA on left everything else signed in.** Enrolling is what a
   customer does when they suspect someone else is in, but other sessions
   and CLI/MCP sign-in keys, none of which passed the new factor, kept
   working.
2. **Turning MFA off took the password alone.** `POST
   /v1/account/mfa/disable` accepted the password from any completed
   session, so a stolen session plus the password removed the second
   factor.
3. **Sign-in keys survived some recoveries.** Operator revoke-sessions,
   operator MFA reset and a confirmed email change ended every session but
   left the 30-day CLI and MCP sign-in keys.
4. **CLI approval minted an admin key from a bare session.** Approving a CLI
   or MCP code turns into an admin-scoped key, but the approval page asked
   for neither a verified email nor a fresh step-up, both of which `POST
   /v1/keys` requires. A copied cookie became a key that outlives the
   session.
5. **Step-up was a no-op without MFA.** `requireStepUp` only asks enrolled
   accounts for a code, so deleting the account and transferring an
   organization's ownership ran on any session of an account without MFA.

## Decision

- **MFA enrollment** signs out every other session (keeping the enrolling
  one), forgets remembered browsers, ends pending grants and revokes
  interactive sign-in keys (`signup`, `cli-login`, MCP login) with reason
  `mfa_enrolled`. Hand-made keys stay.
- **MFA disable with the password** also needs a code verified within the
  5-minute step-up window (`step_up_required` otherwise). The recovery-code
  and emailed-link paths are unchanged.
- **Interactive sign-in keys are revoked** by operator revoke-sessions
  (`operator_revoke`), operator MFA reset (`operator_mfa_reset`) and a
  confirmed email change (`email_changed`).
- **CLI and MCP approval** (`GET`/`POST /cli-auth` for codes) needs a
  verified email, and a step-up within `api.RecentSignInWindow` for an
  enrolled account or a sign-in within it otherwise. A stale browser is sent
  to `/login?reauth=1` with `next` back to the same code.
- **Account deletion and ownership transfer** from a browser session on an
  account without MFA need a sign-in within `api.RecentSignInWindow`.
  Enrolled accounts keep the route's step-up gate; API keys keep their scope
  gate.

## Consequences

- Customers turning MFA on sign in again on their other browsers and run
  `gregale login` again.
- A CLI login from a dashboard tab older than 15 minutes passes through the
  sign-in page once.
- Customers who lost their authenticator disable MFA with a recovery code or
  the emailed link, not the password.
- No migration.
