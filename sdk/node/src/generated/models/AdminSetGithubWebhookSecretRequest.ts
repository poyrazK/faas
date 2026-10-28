/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body shape for POST /v1/admin/github-webhook-secrets. The CLI takes hex so the
 * plaintext never has to be a binary argv value; the API
 * handler hex-decodes before the INSERT.
 *
 */
export type AdminSetGithubWebhookSecretRequest = {
  /**
   * GitHub App installation_id (positive bigint).
   */
  installation_id: number;
  /**
   * Hex-encoded secret (16..64 bytes; 32..128 hex chars).
   */
  secret_hex: string;
};

