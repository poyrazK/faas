# ADR-032 · MVP auth: harden /login against #165 + real sign-in methods

- **Status:** accepted
- **Date:** 2026-07-24
- **Decision:** Replace the magic-link placeholder login with a hardened
  sign-in path (PR #1) and the full real auth surface (PR #2 — email +
  password (Argon2id), Google OAuth with `email_verified`, GitHub OAuth
  login). The PR #1 commit closes issue #165 with the smallest possible
  change; PR #2 lands the password table, OAuth hardening, and the
  end-of-life of the X-Dashboard-Key fallback.
- **Why:** `POST /login` (cmd/apid/handlers_auth.go:74-136, pre-PR #1)
  auto-created an account for any email, minted a `web-console` API key,
  returned the key in the response body, and set a 7-day session cookie
  — with zero verification. A single `curl -d '{"email":"victim"}' /login`
  was a full pre-auth account-takeover (spec §11 violation). The fix
  must (i) close the takeover with a minimal PR for review urgency,
  and (ii) replace the placeholder with a real auth surface in a
  follow-up so we don't ship a "we'll fix it later" intermediate.
- **Consequences:**
  - PR #1 ships a hardened login path that requires both an `email`
    form field AND a valid `X-Dashboard-Key` header (a pre-existing
    "web-console" API key from the buggy pre-#165 deploy). The
    response body never carries an `api_key` field. Unknown email +
    no key → 401 `invalid_credentials`. Email/key mismatch → 401
    `invalid_credentials`. The two failure modes collapse to the
    same body so an attacker cannot probe for valid emails.
  - Six new RFC 7807 stable codes land in `pkg/api/errors.go`:
    `invalid_credentials`, `email_not_verified`, `password_too_weak`,
    `reset_token_invalid`, `reset_token_expired`, `account_exists`.
    Both `invalid_credentials` and `email_not_verified` map to 401;
    the dashboard form renders a single "sign-in failed" copy for
    both so the surface does not leak which case fired.
  - The dashboard now needs a real auth surface. PR #2 lands:
    - Migration `00029_oauth_links` (PK on `(provider, sub)` — the
      §11 anti-takeover invariant: one OAuth subject maps to one
      account, period).
    - Migration `00030_account_passwords` (one row per account;
      OAuth-only accounts have no row).
    - New `pkg/auth/password.go` (Argon2id, PHC string format so
      future parameter bumps don't break old hashes).
    - New `cmd/apid/handlers_auth_login.go` with full email +
      password (Argon2id), `POST /signup`, `POST /login/forgot`,
      `GET /auth/reset`, `POST /auth/reset`,
      `POST /dashboard/account/set-password`.
    - `cmd/apid/handlers_google.go` gates on `email_verified=true`
      and resolves accounts by OAuth subject FIRST (sub-first
      lookup), with email as a fallback for the legacy "account
      created pre-OAuth" case.
    - New `cmd/apid/handlers_github.go` for the GitHub login
      (`/v1/auth/github` sibling of `/v1/auth/google`; the existing
      `/oauth/callback` install-bind stays for already-signed-in
      users).
    - Daily cleanup goroutine in `cmd/apid/main.go` for
      `login_tokens` — the `DeleteOldLoginTokens` Store primitive
      has existed since M7.5 but had no production caller; the
      password-reset flow is the first to use it.
  - PR #3 (polish) lands the "Set a password" dashboard link,
    structured log events for operator observability
    (`event=login_session_issued`, etc.), and the one-off sweep
    of legacy `api_keys where label='web-console'` rows.
- **Rejected alternatives:**
  - *Magic-link only (status quo, hardened):* rejected — the bug
    is in the magic-link dispatcher; rebuilding on it doesn't fix
    the email-verification gap.
  - *Sliding session refresh:* rejected — for an MVP the 7-day
    fixed window matches the CLI's 7-day key TTL and is simpler
    to reason about. A future "remember me" toggle can extend
    the window.
  - *SendGrid-only mail transport:* rejected — keeps the existing
    `FAAS_MAIL_TRANSPORT` env-var seam.
  - *GitHub-only OAuth:* rejected — Google is the largest
    single-provider cohort; cutting it loses too many signups.
  - *bcrypt instead of Argon2id:* rejected — Argon2id is the
    OWASP recommendation for new systems, and the marginal CPU
    cost (one verify per login) is negligible.
  - *PR #1 also blocking on email_verified:* rejected — PR #1
    must close #165 with the minimum surface change; the OAuth
    hardening lands in PR #2 alongside the password table that
    the verification depends on.
  - *Constant-time pad via a single dummy Argon2id hash in
    PR #1:* deferred — `account_passwords` does not exist yet
    in PR #1, so the Argon2id pad has no real branch to equalise.
    The PR #1 path simply does not perform a CPU-heavy operation
    on the no-account branch, which closes the most important
    attack (no account creation, no key mint). PR #2 ships the
    Argon2id pad when the password table lands.
