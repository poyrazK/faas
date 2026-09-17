/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Subscribe a target URL to events emitted by the app. The
 * webhook_secret is HMAC-SHA256 sealed at rest with the host
 * X25519 recipient (namespace `APP_WEBHOOK`).
 *
 */
export type CreateAppWebhookRequest = {
  target_url: string;
  webhook_secret: string;
  event_filter?: Array<'app.parked' | 'app.woken' | 'usage_statement.finalized'>;
  retry_policy?: 'default' | 'aggressive' | 'none';
  /**
   * Wire envelope. json preserves the legacy Gregale body; cloudevents opts into CloudEvents 1.0 structured mode.
   */
  delivery_format?: 'json' | 'cloudevents';
  enabled?: boolean;
};

