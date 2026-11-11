# ADR 20261111150000 · One failed-password budget for every apid and every password check; restoring an account needs a full-access key

- **Status:** accepted
- **Date:** 2026-11-11
- **Related:** ADR-021, ADR-20261108160000, ADR-20261110100000, ADR-20261111110000

## Problem

The 2026-11-10 account-flow audit found:

1. **The per-address budget was per process.** Bug hunt H36 capped failed
   password sign-ins at 20 an hour per email address, counted in each
   apid's memory. With N apids an attacker got N × 20 guesses an hour, and
   every restart or deploy reset the count.
2. **Some password checks were not counted.** The current-password check
   on a password or email change and the password path of turning off
   two-factor authentication verified the password without counting or
   honouring the budget. A stolen browser session could guess the password
   there, limited only by the per-IP bucket.
3. **Restoring an account was a deploy-key action.** `POST
   /v1/account/restore` accepted any key with `deploy:write` and sent no
   mail. A leaked CI key could undo the owner's deletion, which also
   re-enables every key and app the deletion stopped.

## Decision

- **Shared budget.** Failures are counted in Postgres
  (`login_failure_budgets`, one row per keyed email hash, a fixed window of
  `LoginFailureEmailWindow` holding up to `LoginFailuresPerEmail`
  failures). Rows for unbound addresses are kept too, so the 429 does not
  reveal which addresses have accounts. The apid that records a failure
  sweeps up to 200 expired rows. apid type-asserts
  `state.LoginFailureBudgetStore`; when the store cannot be read it falls
  back to the process-local limiter it had before.
- **Every password check shares it.** Sign-in, the MFA enrollment password,
  the current password on a password or email change, and the password
  path of turning off two-factor authentication answer 429 while the
  address is paused, and a wrong password counts against it. A password
  reset still lifts the pause.
- **Restore.** `POST /v1/account/restore` needs a full-access (`admin`) key
  or a browser session and passes the two-factor gate, like `DELETE
  /v1/account`. Every restore (API or dashboard) emails the owner
  (`AccountDeletionCancelledBody`).

## Consequences

- The window is fixed rather than sliding: a guesser can spend up to twice
  the budget across a window boundary, still bounded per hour for the whole
  fleet instead of per process.
- One more write per failed password check; successful sign-ins do not
  touch the table.
- CI keys with only `deploy:write` can no longer cancel a deletion; the
  owner restores from the dashboard or with a full-access key.
- Migration `20261111145205161_login_failure_budget` (expand only).
