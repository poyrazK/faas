/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row in `GET /v1/secrets` — a sealed envelope on a specific
 * app (issue #393). Plaintext NEVER appears here: only the
 * age-sealed envelope (base64). `app_id` and `app_slug` let the
 * dashboard render "foo-app / DATABASE_URL" without a parallel
 * `/v1/apps` round-trip. `scope` (ADR-092 PR-B) carries the
 * env-scope the row belongs to; the account-wide list
 * crosses scopes.
 *
 */
export type AccountAppSecretResponse = {
  app_id: string;
  app_slug: string;
  key: string;
  scope: string;
  /**
   * base64 age-sealed envelope. Plaintext NEVER appears on this wire.
   */
  ciphertext: string;
  created_at: string;
  updated_at: string;
  /**
   * 16-hex HMAC-SHA256(plaintext) keyed by the per-host host.hmac.key (ADR-117 PR-C). Empty for pre-PR-C rows. Mirror of the AccountAppSecretResponse / ScopedAppSecretResponse field.
   */
  value_hash?: string;
};

