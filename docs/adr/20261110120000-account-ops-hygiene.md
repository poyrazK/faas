# ADR 20261110120000 · Account operations: idle sessions, abandoned unverified accounts, sign-up and mail signals, shared TOTP limit

- **Status:** accepted
- **Date:** 2026-11-10
- **Related:** ADR-20261104145353, ADR-20261107103000, ADR-20261108160000, ADR-20261109120000, ADR-20261110100000

## Problem

The 2026-11-08 account-flow audit left four operational gaps:

1. **Dashboard sessions lived 7 days regardless of use.** A cookie left on
   a shared or lost machine stayed valid for a week even if nobody used it.
2. **Never-verified accounts lived forever.** After 30 days they are
   restricted, but a squatted or abandoned signup kept its address, its
   personal org and its Free allowance indefinitely.
3. **No signal when sign-ups or security mail were refused.** The platform
   sign-up cap (`api.SignupAccountsPerDay`) turned away every new customer
   with only an audit row, and a full background mail pool dropped password
   resets and sign-in links with only a log line.
4. **The TOTP guess limit was per apid process.** Ten attempts per account
   per 15 minutes, counted in memory, so N apids allowed 10×N, and a
   restart reset it.

## Decision

- **48-hour idle timeout.** `RequireSessionCookie` ends a session whose
  last request (`sessions.last_seen_at`, else its sign-in) is older than
  `api.DashboardSessionIdleTimeout` (48 h): the row is revoked, the cookie
  cleared, the request answered `401 session_expired`, and
  `auth.session.idle_expired` audited. The 7-day maximum is unchanged.
  `last_seen_at` is touched at most every 5 minutes, which is far finer
  than the timeout.
- **Delete abandoned unverified accounts.** An hourly apid sweep finds
  accounts that are active, unverified, on Free, with no billing customer
  and no app ever created, whose grace clock (signup, or
  `api.EmailVerificationEnforcedFrom` when later) started more than
  `api.UnverifiedAccountDeleteAfter` (60 days) ago, at most
  `api.UnverifiedAccountExpiryBatch` (100) per pass. Each goes through
  `scheduleDeletion` with `via="unverified_expiry"`, exactly like a
  customer's own request: the address is mailed, the 30-day restore window
  applies, and erasure is the usual pipeline. Accounts with an app or
  billing are never touched by the sweep. Concurrent apids are safe:
  scheduling only moves an active account.
- **Metrics and alerts.** `apid_signups_capped_total{scope="network"|"fleet"}`
  counts refused sign-ups; `apid_background_mail_dropped_total` counts
  security mail dropped because the pool was full. Alerts
  `FaasSignupFleetCapReached` and `FaasSecurityMailDropped` (family
  `account_flow`) page with runbooks in `docs/runbooks/`.
- **Shared TOTP attempt limit.** Attempts are counted in
  `account_mail_throttle` under kind `totp_attempt` (10 per 15-minute fixed
  window), shared by every apid; a correct code clears the row
  (`ClearAccountMailSlot`). The in-process guard remains the fallback when
  the store cannot count. Replay protection was already in the database
  (`AcceptTOTPStep`).

## Consequences

- Customers who leave the dashboard for two days sign in again; CLI and API
  keys are unaffected.
- The first unverified deletions can run no earlier than 60 days after
  2026-11-16 (`EmailVerificationEnforcedFrom`).
- A refused TOTP attempt answers `Retry-After` of the whole window (15
  minutes), since the shared counter keeps a fixed window.
- No migration.
