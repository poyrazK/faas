# ADR-20261103202000: Account MFA actions stay in a browser session

Date: 2026-11-03
Status: Accepted
Amends: [ADR-077](077-step-up-mfa.md) (enrollment proof), [ADR-039](039-server-side-session-revocation.md) (MFA recovery entry)

## Context

Account review found that enrollment confirmation accepted an already-used
TOTP code and cleared a pending session, signup omitted the account-wide
password failure counter, and a valid password-reset link could not render.
The CLI's MFA commands used bearer keys against session-only endpoints, and
suspended customers could not obtain the CSRF token needed for recovery.
Organization invitations with activity also failed to reactivate removed
memberships retained for audit.

## Decision

Account MFA commands open `/dashboard/account/mfa` with a closed action name.
The browser proves the account's first factor and submits existing session
and CSRF protected MFA endpoints. Enrollment, confirmation, verification,
recovery and disabling remain session actions. No credential enters the
handoff URL, and no CLI key is exchanged for a browser session. The commands
work before CLI login; JSON and non-interactive output describe the browser
work still required. Legacy secret and QR-output arguments remain parseable
but do not submit credentials or export setup material.

The management page and challenge remain reachable by pending sessions.
Suspended accounts can obtain CSRF tokens; mutations still enforce their
own account status, scope and MFA rules.

Enrollment checks the exact pending factor and commits its timestamp,
policy, revision and spent TOTP step together. Concurrent or already-enrolled
confirmation cannot authenticate another session. Saving setup material
cannot replace a confirmed factor. A newly confirmed session receives a
fresh step-up because it just proved the authenticator. Durable security
alerts remain in the same transaction as enrollment.
MFA actions bypass generic HTTP idempotency response caching: every request
must check its own proof and session, even if a client repeats a request key.

Signup shares login's verification and failure accounting. Reset rendering
carries the token into the form without consuming it. Legacy reset redemption
commits token consumption and the password write together, so a storage failure
leaves the link usable until its original expiry. Invitation acceptance
reactivates only removed membership rows, preserving the existing active
member conflict and recording the new join and activity atomically.

## Validation

Regression checks cover HTTP authentication and recovery, browser handoff
without bearer credentials, concurrent enrollment, stale factor setup,
spent codes and re-invitation against memory and PostgreSQL stores. Reset
regressions cover concurrent redemption, expired links and retrying the same
link after an injected password-write failure.
