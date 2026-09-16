/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An outbound webhook subscription. Carries the masked HMAC secret;
 * the sealed ciphertext is server-side only.
 *
 */
export type AppWebhookResponse = {
  id: string;
  app_id: string;
  account_id: string;
  target_url: string;
  webhook_secret_sealed_masked: '***';
  event_filter: Array<'app.parked' | 'app.woken' | 'usage_statement.finalized'>;
  retry_policy: 'default' | 'aggressive' | 'none';
  delivery_format: 'json' | 'cloudevents';
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

