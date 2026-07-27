/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PasswordLoginRequest } from '../models/PasswordLoginRequest.js';
import type { PasswordLoginResponse } from '../models/PasswordLoginResponse.js';
import type { PasswordResetConfirm } from '../models/PasswordResetConfirm.js';
import type { PasswordResetRequest } from '../models/PasswordResetRequest.js';
import type { PasswordSignupRequest } from '../models/PasswordSignupRequest.js';
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
   * @returns void
   * @throws ApiError
   */
  public static googleAuthRedirect(): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/auth/google',
      errors: {
        302: `Redirect to Google consent.`,
        500: `OAuth misconfigured (GOOGLE_CLIENT_ID unset).`,
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
   * @returns void
   * @throws ApiError
   */
  public static githubAuthRedirect(): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/auth/github',
      errors: {
        302: `Redirect to GitHub consent.`,
        500: `OAuth misconfigured (GITHUB_CLIENT_ID unset).`,
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
      },
    });
  }
  /**
   * Set a password on the authenticated account.
   * Authenticated. Lets OAuth-only customers opt into password
   * login. The same Argon2id path as `/auth/reset`, anchored
   * to the session's account rather than a reset token.
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
        400: `Chosen password is too weak (≥12 chars required).`,
        401: `code: unauthorized`,
      },
    });
  }
}
