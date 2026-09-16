/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Partial update of an existing webhook subscription. Every
 * field is optional — the handler merges the supplied fields
 * onto the current row. omit a field to leave it unchanged.
 *
 */
export type UpdateAppWebhookRequest = {
  target_url?: string;
  webhook_secret?: string;
  event_filter?: Array<'app.parked' | 'app.woken' | 'usage_statement.finalized'>;
  retry_policy?: 'default' | 'aggressive' | 'none';
  /**
   * Wire envelope for future deliveries; existing delivery rows are unchanged.
   */
  delivery_format?: 'json' | 'cloudevents';
  enabled?: boolean;
};

