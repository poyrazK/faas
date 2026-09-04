/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AuthCapabilities } from '../models/AuthCapabilities.js';
import type { CSRFTokenResponse } from '../models/CSRFTokenResponse.js';
import type { MagicLinkSignupRequest } from '../models/MagicLinkSignupRequest.js';
import type { OIDCExchangeRequest } from '../models/OIDCExchangeRequest.js';
import type { OIDCExchangeResponse } from '../models/OIDCExchangeResponse.js';
import type { PasswordLoginRequest } from '../models/PasswordLoginRequest.js';
import type { PasswordLoginResponse } from '../models/PasswordLoginResponse.js';
import type { PasswordResetConfirm } from '../models/PasswordResetConfirm.js';
import type { PasswordResetRequest } from '../models/PasswordResetRequest.js';
import type { PasswordSignupRequest } from '../models/PasswordSignupRequest.js';
import type { ProgrammaticAuthResponse } from '../models/ProgrammaticAuthResponse.js';
import type { SessionListResponse } from '../models/SessionListResponse.js';
import type { SessionsRevokeAllResponse } from '../models/SessionsRevokeAllResponse.js';
import type { SetPasswordRequest } from '../models/SetPasswordRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class AuthService {
  /**
   * Email + password sign-in.
   * Verifies the email + password against the `account_passwords`
   * row (Argon2id, PHC string). Sets the `faas_sid` session
   * cookie on success. The response body carries only
   * `{account_id, plan}` — no API key. The SDK's device-code
   * flow is the programmatic-auth path; the dashboard cookie is
   * the only auth artifact on the browser side.
   *
   * Anti-enumeration: unbound email, wrong password, and
   * passwordless (OAuth-only) accounts all return 401
   * `invalid_credentials` with the same body. Every code path
   * runs one Argon2id verify under identical parameters so the
   * timing oracle stays closed.
   *
   * @returns PasswordLoginResponse Signed in. The `Set-Cookie: faas_sid=…` header carries
   * the session cookie.
   *
   * @throws ApiError
   */
  public static passwordLogin({
    requestBody,
  }: {
    requestBody: PasswordLoginRequest,
  }): CancelablePromise<PasswordLoginResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/login',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: invalid_credentials — wrong email or password. Drafted for the dashboard auth surface (issue #165 PR #2, ADR-032).`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Create account with email + password.
   * Creates an account and signs the caller in. On a colliding
   * email + matching password, the call is idempotent and signs
   * in. On a colliding email + different password, the response
   * is 401 `invalid_credentials` (never 409 — the surface
   * forbids account enumeration).
   *
   * Password floor: 12 characters (NIST-style; no complexity
   * rules).
   *
   * @returns PasswordLoginResponse Account created (or reused) and signed in.
   * @throws ApiError
   */
  public static passwordSignup({
    requestBody,
  }: {
    requestBody: PasswordSignupRequest,
  }): CancelablePromise<PasswordLoginResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/signup',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Password too weak (≥12 chars required).`,
        401: `code: invalid_credentials — wrong email or password. Drafted for the dashboard auth surface (issue #165 PR #2, ADR-032).`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Request a password-reset email.
   * Always returns 200 with the same body regardless of
   * whether the email is bound to an account. The reset URL
   * is mailed via the platform's outbound mailer; the response
   * never leaks account presence.
   *
   * @returns any Request accepted. The reset email is sent if the address is registered.
   * @throws ApiError
   */
  public static passwordForgot({
    requestBody,
  }: {
    requestBody?: PasswordResetRequest,
  }): CancelablePromise<{
    status: 'ok';
  }> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/login/forgot',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Programmatic signup (JSON-only, bearer-key CLI path).
   * Issue #311 / `gregale signup` — JSON-only endpoint that
   * creates an account (or signs the caller in idempotently),
   * mints a fresh programmatic API key, and returns the
   * `ProgrammaticAuthResponse` payload. The CLI persists the
   * plaintext via `saveToken()` without a dashboard round-trip.
   *
   * Anti-enumeration posture mirrors `/signup`:
   * - email unbound: create + set password + mint key.
   * - email bound + same password: idempotent sign-in + mint key.
   * - email bound + different password: 401 `invalid_credentials`.
   * No Set-Cookie header; bearer-key only.
   *
   * @returns ProgrammaticAuthResponse Account created (or reused) + freshly minted API key.
   * The plaintext is returned ONCE; the caller persists it.
   *
   * @throws ApiError
   */
  public static programmaticSignup({
    requestBody,
  }: {
    requestBody: PasswordSignupRequest,
  }): CancelablePromise<ProgrammaticAuthResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/auth/signup',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: invalid_credentials — wrong email or password. Drafted for the dashboard auth surface (issue #165 PR #2, ADR-032).`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Programmatic login (JSON-only, bearer-key CLI path).
   * Issue #311 / `gregale login` mirror — JSON-only endpoint
   * that authenticates an email + password and returns a
   * `ProgrammaticAuthResponse` payload. Same response shape as
   * `/v1/auth/signup` so the CLI can reuse its unmarshaler.
   *
   * Anti-enumeration posture mirrors `/login`: Argon2id pad
   * on the no-row branch, identical 401 on wrong-password vs
   * unbound email.
   *
   * @returns ProgrammaticAuthResponse Authenticated + freshly minted API key.
   * @throws ApiError
   */
  public static programmaticLogin({
    requestBody,
  }: {
    requestBody: PasswordLoginRequest,
  }): CancelablePromise<ProgrammaticAuthResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/auth/login',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: invalid_credentials — wrong email or password. Drafted for the dashboard auth surface (issue #165 PR #2, ADR-032).`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Magic-link signup (JSON-only, no password).
   * Issue #311 / `gregale signup --email-only EMAIL` — emails
   * a one-time signup link to the given address. Always
   * returns 200 with the same body regardless of whether the
   * email is bound, unbound, malformed, or missing in the
   * request — the response cannot be used to enumerate
   * accounts.
   *
   * On a real-account hit, the handler creates the account if
   * unbound, mints a 32-byte token, persists its SHA-256 via
   * `IssueLoginToken` (15-minute TTL), and emails the
   * `/auth/verify?token=...` link through the platform mailer.
   *
   * @returns any Request accepted. The signup link is mailed if the address
   * is recognised (or could be registered).
   *
   * @throws ApiError
   */
  public static programmaticSignupMagicLink({
    requestBody,
  }: {
    requestBody: MagicLinkSignupRequest,
  }): CancelablePromise<{
    status: 'ok';
  }> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/auth/signup/magic-link',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Render the password-reset form.
   * Validates the token shape; renders the
   * `password_reset_form.html` template on a valid token.
   * Invalid / expired / consumed tokens return 410 Gone.
   *
   * @returns string Token is valid; renders the form.
   * @throws ApiError
   */
  public static passwordResetForm({
    token,
  }: {
    /**
     * Base64url-encoded 32-byte token from the reset email.
     *
     */
    token: string,
  }): CancelablePromise<string> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/auth/reset',
      query: {
        'token': token,
      },
      errors: {
        410: `code: reset_token_invalid | reset_token_expired — the password-reset token is unknown, already consumed, or expired.`,
      },
    });
  }
  /**
   * Submit a new password against a reset token.
   * Atomically consumes the token (one-shot, replay returns
   * 410), Argon2id-encodes the new password, sets it on the
   * account, and signs the caller in.
   *
   * @returns void
   * @throws ApiError
   */
  public static passwordResetConfirm({
    formData,
  }: {
    formData: PasswordResetConfirm,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/auth/reset',
      formData: formData,
      mediaType: 'application/x-www-form-urlencoded',
      errors: {
        302: `Password set. Redirects to \`/dashboard/\`. \`Set-Cookie:
        faas_sid=…\` is set.
        `,
        400: `New password is too weak (≥12 chars required).`,
        410: `code: reset_token_invalid | reset_token_expired — the password-reset token is unknown, already consumed, or expired.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * GitHub-style Google OAuth 2.0 consent redirect.
   * Sets a 16-byte CSRF state cookie scoped to
   * `/v1/auth/google/callback` and 302s to the Google consent
   * screen. The callback consumes the state cookie, exchanges
   * the code at `oauth2.googleapis.com/token`, fetches the
   * userinfo, and verifies `email_verified=true` before
   * minting a session (issue #165 PR #2, ADR-032).
   *
   * Returns 503 `oauth_provider_unavailable` when the operator
   * did not configure Google sign-in on this host (both
   * `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` unset at
   * boot, issue #419 / ADR-046). The same shape is returned
   * for the half-set case, which fails to boot.
   *
   * @returns void
   * @throws ApiError
   */
  public static googleAuthRedirect(): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/auth/google',
      errors: {
        302: `Redirect to Google consent.`,
        500: `Google OAuth misconfigured at runtime. Defensive
        catch — boot validation (ADR-046) should make this
        unreachable in production.
        `,
        503: `Google sign-in not configured on this host. Operator
        must set both \`GOOGLE_CLIENT_ID\` and
        \`GOOGLE_CLIENT_SECRET\` and restart apid (ADR-046).
        Surfaces as \`oauth_provider_unavailable\`.
        `,
      },
    });
  }
  /**
   * Google OAuth 2.0 callback.
   * Verifies state, exchanges the code, fetches the profile,
   * enforces `email_verified=true`, and signs the user in.
   * Sub-first lookup against `oauth_links` enforces the §11
   * anti-takeover invariant.
   *
   * @returns void
   * @throws ApiError
   */
  public static googleAuthCallback({
    code,
    state,
  }: {
    /**
     * Authorization code returned by Google on the consent
     * redirect. Single-use; exchanged at
     * `oauth2.googleapis.com/token`.
     *
     */
    code: string,
    /**
     * CSRF state token. Must match the `faas_google_state`
     * cookie set on the consent redirect. Mismatch returns
     * 401 `csrf_mismatch`.
     *
     */
    state: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/auth/google/callback',
      query: {
        'code': code,
        'state': state,
      },
      errors: {
        302: `Redirect to \`WEBSITE_URL\` or \`/\`. \`Set-Cookie:
        faas_sid=…\` is set.
        `,
        401: `Google email not verified at the upstream.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
        503: `OAuth provider not configured on this host (stale
        cookie or direct callback hit before operator wired
        \`GOOGLE_CLIENT_ID\`/\`GOOGLE_CLIENT_SECRET\`).
        Surfaces as \`oauth_provider_unavailable\`.
        `,
      },
    });
  }
  /**
   * GitHub OAuth 2.0 consent redirect.
   * Sets a 16-byte CSRF state cookie scoped to
   * `/v1/auth/github/callback` and 302s to the GitHub consent
   * with `scope=read:user user:email`. The callback requires
   * a primary && verified email before minting a session
   * (issue #165 PR #2, ADR-032).
   *
   * Returns 503 `oauth_provider_unavailable` when the operator
   * did not configure GitHub sign-in on this host (both
   * `GITHUB_CLIENT_ID` and `GITHUB_CLIENT_SECRET` unset at
   * boot, issue #419 / ADR-046).
   *
   * @returns void
   * @throws ApiError
   */
  public static githubAuthRedirect(): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/auth/github',
      errors: {
        302: `Redirect to GitHub consent.`,
        500: `GitHub OAuth misconfigured at runtime. Defensive
        catch — boot validation (ADR-046) should make this
        unreachable in production.
        `,
        503: `GitHub sign-in not configured on this host. Operator
        must set both \`GITHUB_CLIENT_ID\` and
        \`GITHUB_CLIENT_SECRET\` and restart apid (ADR-046).
        Surfaces as \`oauth_provider_unavailable\`.
        `,
      },
    });
  }
  /**
   * GitHub OAuth 2.0 callback.
   * Verifies state, exchanges the code, fetches `/user` and
   * `/user/emails`, filters the primary && verified email,
   * and signs the user in. Sub-first lookup against
   * `oauth_links` enforces the §11 anti-takeover invariant.
   *
   * On success, `auth.login` is appended to the events table
   * (ADR-035) with `method=github`, `email=<verified email>`,
   * and `login=<GitHub username>` so the audit dashboard and
   * the corroborating slog line reference one identifier.
   *
   * @returns void
   * @throws ApiError
   */
  public static githubAuthCallback({
    code,
    state,
  }: {
    /**
     * Authorization code returned by GitHub on the consent
     * redirect. Single-use; exchanged at
     * `github.com/login/oauth/access_token`.
     *
     */
    code: string,
    /**
     * CSRF state token. Must match the `faas_github_state`
     * cookie set on the consent redirect. Mismatch returns
     * 400 `csrf_mismatch`.
     *
     */
    state: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/auth/github/callback',
      query: {
        'code': code,
        'state': state,
      },
      errors: {
        302: `Redirect to \`WEBSITE_URL\` or \`/\` after a successful
        GitHub OAuth handshake. \`Set-Cookie: faas_sid=…\` is set.
        `,
        400: `Malformed callback. The \`code\` field maps to
        \`invalid_state\` (no CSRF cookie), \`csrf_mismatch\`
        (query \`state\` does not match the cookie), or
        \`missing_code\` (no \`code\` query param). The
        \`oauth_exchange_failed\` code signals the upstream
        GitHub token exchange returned a non-JSON body
        (rarely seen post-2024; the handler sends
        \`Accept: application/json\`).
        `,
        401: `GitHub account has no primary && verified email.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
        500: `OAuth misconfigured server-side (\`GITHUB_CLIENT_ID\`
        or \`GITHUB_CLIENT_SECRET\` unset). Surfaces as
        \`github_oauth_misconfigured\`.
        `,
        502: `\`github.com/login/oauth/access_token\`,
        \`api.github.com/user\`, or \`api.github.com/user/emails\`
        unreachable. Surfaces as \`github_unreachable\`.
        `,
        503: `OAuth provider not configured on this host (stale
        cookie or direct callback hit before operator wired
        \`GITHUB_CLIENT_ID\`/\`GITHUB_CLIENT_SECRET\`).
        Surfaces as \`oauth_provider_unavailable\`.
        `,
      },
    });
  }
  /**
   * Sign-in OAuth capability signal for the dashboard.
   * Returns the boot-resolved OAuth provider state for this apid
   * host (issue #419 / ADR-046). The dashboard reads this on
   * `/login` to decide whether to render the "Sign in with
   * Google" / "Sign in with GitHub" buttons.
   *
   * Mounted behind the dashboard's session-cookie auth — a
   * scanner without a session gets 302 to `/login` first, so
   * this is not a brute-force amplification surface even though
   * it surfaces provider enablement. The set of provider names
   * is closed (`google`, `github`); future providers land as
   * new keys, not by adding a list.
   *
   * `enabled=true` means the provider's `/v1/auth/<provider>`
   * consent route will issue a 302 to the upstream consent
   * screen on a fresh request. `enabled=false` means it will
   * return 503 `oauth_provider_unavailable`.
   *
   * @returns AuthCapabilities Per-provider enabled signal.
   * @throws ApiError
   */
  public static getAuthCapabilities(): CancelablePromise<AuthCapabilities> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/auth/capabilities',
      errors: {
        302: `No valid session cookie; redirected to /login.`,
      },
    });
  }
  /**
   * Log the current dashboard session out.
   * Revokes the calling session row in the `sessions` table
   * (one row per dashboard login; ADR-039 / IAM-3) and
   * clears the `faas_sid` cookie. Always 204 on success,
   * even if the row was already revoked (idempotent — the
   * cookie is cleared either way). Other sessions on the
   * same account are NOT touched; use
   * `POST /v1/auth/sessions/revoke_all` for that.
   *
   * CSRF: action `logout` (verify via the
   * `faas_csrf` cookie + body `csrf_token` field or
   * `X-CSRF-Token` header).
   *
   * Emits `auth.session.revoke` with
   * `reason: "logout"`.
   *
   * @returns void
   * @throws ApiError
   */
  public static postAccountLogout({
    faasSid,
  }: {
    /**
     * Dashboard session cookie. Sealed; opaque to the client
     * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
     * The browser sets it automatically on `/login` / `/signup`;
     * the SDK uses the device-code flow instead and never sets
     * this cookie.
     *
     */
    faasSid?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/auth/logout',
      cookies: {
        'faas_sid': faasSid,
      },
      errors: {
        400: `CSRF check failed.`,
        401: `Cookie missing, expired, tampered, or its \`sid\` is
        not backed by an active sessions row. The cookie
        is cleared on the wire. Surfaces as
        \`session_expired\` (pre-IAM-3 cookie / missing /
        revoked) or \`session_invalid\` (account-mismatch
        defensive path; AEAD-bound envelopes should not
        produce this).
        `,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Issue an action-bound CSRF token for the dashboard.
   * Returns a short-lived CSRF token bound to the authenticated
   * account and the requested browser mutation. The matching
   * `faas_csrf` cookie is HttpOnly; clients send the returned
   * `csrf_token` in the mutation's JSON body. This route remains
   * reachable while the session is `mfa_pending` so the dashboard
   * can complete MFA enrollment or recovery.
   *
   * @returns CSRFTokenResponse Action-bound CSRF token.
   * @throws ApiError
   */
  public static issueBrowserCsrfToken({
    action,
    faasSid,
  }: {
    /**
     * Exact mutation action the token will authorize.
     */
    action: 'auth.logout' | 'auth.session.revoke' | 'auth.sessions.revoke_all' | 'mfa_confirm' | 'mfa_recover' | 'mfa_disable' | 'set_password',
    /**
     * Dashboard session cookie. Sealed; opaque to the client
     * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
     * The browser sets it automatically on `/login` / `/signup`;
     * the SDK uses the device-code flow instead and never sets
     * this cookie.
     *
     */
    faasSid?: string,
  }): CancelablePromise<CSRFTokenResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/auth/csrf',
      cookies: {
        'faas_sid': faasSid,
      },
      query: {
        'action': action,
      },
      errors: {
        400: `Unknown or missing action.`,
        401: `code: unauthorized`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * List active sessions for the calling account.
   * Returns one `SessionInfo` row per active login (one row
   * per dashboard login; ADR-039 / IAM-3). Newest first. The
   * row whose `id` matches the calling cookie's `sid` is
   * flagged `current_session: true`. Revoked rows are NOT
   * returned; the `audit-events` endpoint is the timeline
   * for those.
   *
   * @returns SessionListResponse Active sessions for the calling account.
   * @throws ApiError
   */
  public static getAccountSessions({
    faasSid,
  }: {
    /**
     * Dashboard session cookie. Sealed; opaque to the client
     * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
     * The browser sets it automatically on `/login` / `/signup`;
     * the SDK uses the device-code flow instead and never sets
     * this cookie.
     *
     */
    faasSid?: string,
  }): CancelablePromise<SessionListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/auth/sessions',
      cookies: {
        'faas_sid': faasSid,
      },
      errors: {
        401: `See \`/v1/auth/logout\` — same 401 surface.
        `,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Revoke a single session by id.
   * Revokes the `sessions` row whose `id` is `{id}` and
   * whose `account_id` matches the calling envelope. Cross-
   * account DELETE returns 404 (not 403) — the handler
   * never confirms a row exists in another account. Revoking
   * the current session is allowed: the calling cookie is
   * cleared on the wire (same as `/v1/auth/logout`).
   *
   * CSRF: action `session_revoke`.
   *
   * Emits `auth.session.revoke` with
   * `reason: "explicit"`.
   *
   * @returns void
   * @throws ApiError
   */
  public static deleteAccountSession({
    id,
    requestBody,
    faasSid,
  }: {
    /**
     * Session id (UUID v4) from `SessionInfo.id`.
     */
    id: string,
    requestBody: {
      csrf_token: string;
    },
    /**
     * Dashboard session cookie. Sealed; opaque to the client
     * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
     * The browser sets it automatically on `/login` / `/signup`;
     * the SDK uses the device-code flow instead and never sets
     * this cookie.
     *
     */
    faasSid?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/auth/sessions/{id}',
      path: {
        'id': id,
      },
      cookies: {
        'faas_sid': faasSid,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `CSRF check failed (\`csrf_mismatch\`) or the path id
        is not a valid UUID (\`validation_failed\`).
        `,
        401: `See \`/v1/auth/logout\`.`,
        404: `No active session matches the id on this account
        (does not exist, already revoked, or belongs to
        another account). The 404 is the same shape
        regardless — we never leak existence across
        accounts.
        `,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Revoke every session except the calling one.
   * Revokes all `sessions` rows for the calling account
   * whose `id` is NOT the calling cookie's `sid`. The
   * caller's session stays active. Returns the count of
   * rows revoked so the dashboard can render "Signed out
   * N other devices".
   *
   * CSRF: action `sessions_revoke_all`.
   *
   * Emits `auth.sessions.revoke_all` with
   * `{revoked_count, retained_sid}`.
   *
   * @returns SessionsRevokeAllResponse Bulk revocation succeeded.
   * @throws ApiError
   */
  public static postAccountSessionsRevokeAll({
    requestBody,
    faasSid,
  }: {
    requestBody: {
      csrf_token: string;
    },
    /**
     * Dashboard session cookie. Sealed; opaque to the client
     * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
     * The browser sets it automatically on `/login` / `/signup`;
     * the SDK uses the device-code flow instead and never sets
     * this cookie.
     *
     */
    faasSid?: string,
  }): CancelablePromise<SessionsRevokeAllResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/auth/sessions/revoke_all',
      cookies: {
        'faas_sid': faasSid,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `CSRF check failed on the bulk-revoke request.`,
        401: `Cookie missing, expired, tampered, or its \`sid\` is not backed by an active sessions row. Same 401 surface as \`/v1/auth/logout\`.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Exchange an IdP-issued JWT for a short-lived deploy bearer
   * ADR-101 / issue #270. CI runners that have an IdP-issued OIDC
   * JWT (RFC 8414; e.g. GitHub Actions `ACTIONS_ID_TOKEN_REQUEST_TOKEN`,
   * GitLab CI, CircleCI) call this endpoint to exchange it for a
   * short-lived opaque bearer (5 min TTL, `fp_oidc_<48 hex>` prefix).
   * The bearer is then used in `Authorization: Bearer …` on the
   * existing deploy routes.
   *
   * The endpoint is anonymous — the JWT is the auth — so it does
   * not require a session or a previous bearer. The first-use
   * auto-create flow bootstraps a permissive trust policy on the
   * `(account_id, issuer_url)` pair so customers do not have to
   * configure the dashboard before their first CI deploy.
   *
   * The AuthLimit surface is the shared per-IP bucket (spec §11
   * 10/min/IP) — high-volume CI runners may hit the cap; long-lived
   * deploy tokens remain the escape hatch.
   *
   * @returns OIDCExchangeResponse Exchanged. The bearer is in the response body.
   * @throws ApiError
   */
  public static oidcExchange({
    requestBody,
  }: {
    requestBody: OIDCExchangeRequest,
  }): CancelablePromise<OIDCExchangeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/auth/oidc/exchange',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Malformed request body or empty fields.`,
        401: `JWT signature / issuer / audience / subject failed verification, OR no account is bound to the (issuer, subject) pair.`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Set or replace the password on the authenticated account.
   * Authenticated. Lets OAuth-only customers opt into password
   * login and lets customers who already have a password replace
   * it. The same Argon2id path as `/auth/reset`, anchored to the
   * session's account rather than a reset token.
   *
   * The form must carry a `csrf_token` minted by
   * `GET /v1/auth/csrf?action=set_password` (double-submit with
   * the `faas_csrf` cookie); a missing or mismatched token is 400
   * `validation_failed`. The route is a same-site form POST, so
   * `SameSite=Lax` alone does not protect it from a customer app
   * hosted under `*.apps.gregale.dev`.
   *
   * Proof of presence (ADR-140), decided by what the account has:
   *
   * - A step-up verified within the last 5 minutes
   * (`POST /v1/account/mfa/verify`) is accepted as-is.
   * - Otherwise, if an explicit `mfa_required` policy is armed
   * while the account has not enrolled, 403 `mfa_required`.
   * This is a policy hook; MFA remains opt-in for ordinary
   * accounts.
   * - Otherwise, if the account has MFA enrolled, 403
   * `step_up_required` — verify TOTP first, whether or not the
   * account also has a password.
   * - Otherwise, if the account already has a password,
   * `current_password` is required and verified. Missing and
   * wrong both answer 401 `invalid_credentials`.
   * - Otherwise (OAuth-only, no MFA) the request is accepted; the
   * session is the only proof the account has.
   *
   * @returns void
   * @throws ApiError
   */
  public static setPassword({
    formData,
    faasSid,
  }: {
    formData: SetPasswordRequest,
    /**
     * Dashboard session cookie. Sealed; opaque to the client
     * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
     * The browser sets it automatically on `/login` / `/signup`;
     * the SDK uses the device-code flow instead and never sets
     * this cookie.
     *
     */
    faasSid?: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/dashboard/account/set-password',
      cookies: {
        'faas_sid': faasSid,
      },
      formData: formData,
      mediaType: 'application/x-www-form-urlencoded',
      errors: {
        302: `Password set. Redirects to \`/dashboard/account/\`.`,
        400: `Chosen password is too weak (\`password_too_weak\`, ≥12
        chars required), or the \`csrf_token\` is missing or does
        not match the \`faas_csrf\` cookie (\`validation_failed\`).
        `,
        401: `No session, or the account has a password and
        \`current_password\` is missing or wrong
        (\`invalid_credentials\`).
        `,
        403: `The account has MFA enrolled and the session carries no
        step-up from the last 5 minutes (\`step_up_required\`), or
        an explicit \`mfa_required\` policy is pending enrollment
        (\`mfa_required\`). MFA is opt-in for ordinary accounts.
        `,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
}
