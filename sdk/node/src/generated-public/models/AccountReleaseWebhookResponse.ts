/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account-owned release receiver; no app_id because it follows all current and future apps.
 */
export type AccountReleaseWebhookResponse = {
  id: string;
  scope: 'account';
  account_id: string;
  target_url: string;
  webhook_secret_sealed_masked: '***';
  event_filter: Array<'deployment.live' | 'deployment.failed' | 'rollout.completed' | 'rollout.aborted'>;
  retry_policy: 'default' | 'aggressive' | 'none';
  delivery_format: 'json' | 'cloudevents';
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

