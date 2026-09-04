# ADR-140 · Proof of presence for `POST /dashboard/account/set-password`

- **Status:** proposed
- **Date:** 2026-09-03
- **Decision:** The set-password handler chooses its own proof of presence from what the account has — a fresh step-up, an explicit pending MFA policy, the current password, or (for an OAuth-only account with no MFA) the session itself — instead of sitting behind the blanket `requireStepUpHandler(5m)` mount from ADR-077. MFA remains opt-in for ordinary accounts.
- **Why:** The only writer of a step-up stamp is `POST /v1/account/mfa/verify`, so the blanket gate meant an OAuth-only customer without MFA — the customer the route exists for — could never set a password (403 `step_up_required` on every attempt). At the same time, a customer who *had* a password and *had* stepped up could replace it without re-proving anything; the knowledge factor was never consulted.
- **Consequences:** `SetPasswordRequest` gains an optional `current_password`. Two new audit kinds: `account.password_set` (with `proof` ∈ {`step_up`, `current_password`, `session`} and `replaced`, true when an existing password was overwritten) and `account.password_set_denied`. The ADR-077 routes table row for this path is superseded by this ADR. The console gets a "current password" step in a follow-up, ideally with a `has_password` field on `GET /v1/account` so it can decide up front.
- **Rejected alternatives:** (a) keep the blanket step-up and require every customer to enrol MFA before setting a password — makes the OAuth opt-in unusable for the majority; (b) drop the gate and verify nothing — reopens "stolen browser sets a password" for every account; (c) re-run the OAuth consent as the proof for OAuth-only accounts — no server surface for it today, and the consent redirect cannot be completed from a fetch.

## Context

`POST /dashboard/account/set-password` was introduced (ADR-032 PR #2)
as the opt-in for customers who signed up through Google or GitHub
and want email + password as well. ADR-077 later mounted it behind
`sessionAuth → requireStepUpHandler(5m)` alongside account deletion,
closing the "stolen browser, post-MFA-clear" threat.

Two facts make that mount wrong for this particular route:

1. `pkg/session` only ever receives a step-up stamp from
   `cmd/apid/handlers_mfa.go::mfaVerify` (`reissueSessionCookieWithStepUp(…, time.Now())`).
   `sessionAuth` stamps `env.StepUpAt` onto every cookie request, so
   `StepUpFrom` reports `has=true, ts=zero` for any session that has
   not verified TOTP in the last five minutes, and the gate answers
   403 `step_up_required`. A customer with no MFA enrolled has no way
   to obtain a stamp. The route was therefore unreachable for exactly
   the customers it was built for.
2. `postSetPassword` never read the existing `account_passwords` row.
   For a customer who already had a password, a fresh step-up was
   sufficient to replace it — the knowledge factor that `/login`,
   `/v1/account/mfa/disable` (`disableByPassword`) and account
   deletion all re-verify was skipped here.

Neither surfaced in production because the customer console
(`poyrazK/faas-web`) did not route the POST to `apid` at all — the
request hit the SPA fallback and the old panel reported the resulting
`200 index.html` as success. That routing bug is fixed on the console
side; this ADR fixes the server side it exposed.

## Decision

`cmd/apid/handlers_auth_login.go::postSetPassword` calls
`setPasswordProof`, which decides in this order and stops at the first
match:

| Account state | Session state | Outcome | Audit `proof` |
|---|---|---|---|
| any | step-up stamp ≤ 5 min | accept | `step_up` |
| `MFARequired=true`, not enrolled | no fresh stamp | 403 `mfa_required`; complete enrollment | — |
| MFA enrolled (with or without a password) | no fresh stamp | 403 `step_up_required` (same problem and audit row the ADR-077 gate emitted, including its `missing`/`expired` split) | — |
| has password, no MFA | no fresh stamp | require `current_password`; `auth.Verify` against the stored PHC; missing or wrong → 401 `invalid_credentials` | `current_password` |
| no password, no MFA | any | accept | `session` |

The 5-minute TTL is `setPasswordStepUpTTL`, the same window every
other ADR-077 route uses.

The second factor outranks the knowledge factor on purpose. An
MFA-enrolled customer who also has a password stays on the ADR-077
tier: a phished password plus a stolen session is not enough to rotate
the password. (`/v1/account/mfa/disable` does accept the password
alone, but that is a recovery path for a lost authenticator, not a
precedent for routine changes.) The length rule on the new password
runs before any of this — it is free and inspects only the new value,
so a request that could never be accepted costs no DB read and no
Argon2id.

Missing and wrong `current_password` are deliberately the same 401.
The caller is already authenticated as the account, so there is
nothing to enumerate; the shared answer keeps the handler from
becoming an oracle for "does this account have a password" to a
script running in a stolen session. `auth.Verify` is called on the
stored hash in both cases so the two paths cost the same.

The `session` row is the one that accepts without a second factor.
That is a conscious posture, not an omission: an OAuth-only account
with no MFA has no factor the server could re-verify short of
re-running the provider consent, and a session obtained through that
consent is the strongest proof the account can currently offer. It is
the same trust the account already extends to every other
session-authenticated write. The audit row makes it visible; the
follow-up that adds `has_password` to `GET /v1/account` lets the
console explain it.

The mount in `cmd/apid/server.go` drops `requireStepUpHandler`; the
handler emits the identical `auth.step_up_required` audit row on the
MFA-enrolled branch so ADR-077's downstream queries keep working.

### Rate limit

`current_password` is a credential check, so the route is mounted
through `dashboardAuthChain` and shares the dashboard's per-IP failure
bucket with `/login` (§11: 10 failures per minute per IP, counting the
401s). Without it a stolen session would be a free oracle for guessing
the password. The ADR-077 mount had no limiter because it accepted no
credential.

### CSRF

The route is a form POST authenticated by the `faas_sid` cookie, which
is `SameSite=Lax`. Customer functions are served from
`*.apps.gregale.dev`, which is *same-site* with `api.gregale.dev`, so a
form auto-submitted by a customer-hosted page still carries the
victim's session cookie. The blanket step-up gate incidentally blocked
that; the `session` row above would not. So every branch — including
the ones that go on to verify `current_password` or a step-up — first
requires a purpose-bound `csrf_token` (`middleware.VerifyAuthenticated`,
action `set_password`), minted by `GET /v1/auth/csrf?action=set_password`
and double-submitted with the `faas_csrf` cookie. Missing or mismatched
is 400 `validation_failed`, the same answer `dashboardDelete` gives.
`set_password` joins the closed `csrfActions` allowlist.

## Wire

`SetPasswordRequest` (form-encoded):

```
password          string  required, 12–256 chars
csrf_token        string  required; from GET /v1/auth/csrf?action=set_password
current_password  string  optional; required by the "has password" row
```

Responses: `302` → `/dashboard/account/` on success; `400`
`validation_failed` (CSRF) or `password_too_weak`; `401`
`invalid_credentials` (also the no-session answer); `403`
`step_up_required` for enrolled MFA or `mfa_required` for an explicit
pending policy; `429` after ten 401s from one IP in a minute.

## Tests

`cmd/apid/set_password_test.go` pins the full matrix: OAuth-only
no-MFA accepts without a stamp; an explicit pending MFA policy gets
403 without enrollment; has-password refuses a missing and a wrong
`current_password` (401, stored hash untouched) and accepts the right
one; a fresh stamp stands in for `current_password`; MFA-enrolled
without a password or a stamp gets 403; the length rule still applies
after a valid proof.

## Follow-ups

- **Sibling mounts.** `POST /dashboard/account/delete` and
  `POST /dashboard/raise-overage-cap` sit behind the same
  `requireStepUpHandler(5m)` mount and lock out no-MFA customers from
  the dashboard in the same way (the `/v1` equivalents remain reachable
  with an API key). Same diagnosis, same shape of fix — deliberately
  not folded into this PR so each route's threat model gets its own
  read; needs its own ADR or an amendment here.
- **Sessions survive a password change.** Neither this route nor
  `/auth/reset` revokes other sessions after `SetAccountPassword`, and
  there is no credential epoch on the session envelope — so a session
  an attacker already holds outlives the rotation that was meant to
  evict them. Fix belongs to both paths at once (revoke all but the
  current session, or stamp an epoch that `sessionAuth` compares);
  separate PR.
- `GET /v1/account` → `has_password: bool`, so the console can render
  the "current password" step only when it applies.
- Console (`faas-web`): insert that step ahead of the choose/confirm
  wizard and send `current_password`.
- Consider a "password added to your account" mail on the `session`
  proof path, mirroring the notification most providers send.
