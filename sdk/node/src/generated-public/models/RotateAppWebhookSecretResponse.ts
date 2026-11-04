/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Response from POST /v1/apps/{slug}/webhooks/{id}/rotate-secret.
 * The plaintext secret is NEVER returned; the masked field is
 * a constant sentinel so the dashboard renders the same shape
 * across all secret-bearing rows (mirrors AlertRule rotate).
 *
 */
export type RotateAppWebhookSecretResponse = {
  rotated_at: string;
  webhook_secret_sealed_masked: '***';
};

