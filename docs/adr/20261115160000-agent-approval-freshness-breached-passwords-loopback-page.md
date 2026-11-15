# ADR-20261115160000 · Fresh sign-in for agent approvals, breached-password list, one-click login page

- **Status:** accepted
- **Date:** 2026-11-15
- **Amends:** ADR-20261104140058 (agent approvals), ADR-032 (passwords), ADR-20261111170000 (one-click CLI login)
- **Context:** The 2026-11-10 account-flow audit found:
  - F12-2: an agent's delete, production, credentials or billing request could be approved from any signed-in browser, however old the session, while approving a CLI sign-in needs a recent sign-in.
  - PR I: new passwords were checked for length only, so `password1234` was accepted.
  - F10-3: the one-click login page sends a fresh device code to `http://127.0.0.1:<port>/callback`, with the port taken from the link. A link naming the port of a local program that logs request URLs leaks a redeemable device code.
  - F13-2 (dashboard plan change without step-up) was checked and is a false positive: `dashboardAccountPlan` already wraps every real plan change in `requireStepUpHandler`; only an unchanged plan or a preview is exempt.
- **Decision:**
  1. Approving (not denying) an agent request in the delete, production, credentials or billing category needs `cliApprovalFresh`: a two-factor code within `RecentSignInWindow` when the account has two-factor on, else a sign-in within it. A stale session is redirected to sign in again and returns to the same review page.
  2. `auth.Validate` refuses passwords on a bundled list of common breached passwords (`pkg/auth/breached_passwords.txt`, embedded, matched case-insensitively), one repeated character, and plain ascending or descending runs. No network lookup. `auth.Encode` keeps checking length only, since it is the hashing primitive; every path that takes a new password runs `Validate` first.
  3. The one-click approval page shows the login's code and the callback port and tells the person to continue only when their terminal shows the same; `gregale login` prints both. Binding the exchange to a secret held by the minting CLI was rejected: in the attack the person who sent the link minted the code, so they hold that secret. Moving the device code to a POST body was rejected: existing CLIs accept only GET, and the page's CSP blocks a cross-origin form post.
- **Consequences:**
  - A week-old session can still deny an agent but must sign in again to approve a risky request.
  - Customers who pick a listed password are asked for another; existing passwords keep working.
  - F10-3 is reduced to a person approving a link whose code their terminal does not show; it is not closed in code. Showing where and when the code was requested (IP, time) needs a new column and is left for later.
  - Release skew: no schema change; older CLIs keep working with the new page.
