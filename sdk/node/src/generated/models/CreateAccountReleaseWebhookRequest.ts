/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Subscribe a target to release events emitted by every app owned by the active account.
 */
export type CreateAccountReleaseWebhookRequest = {
  target_url: string;
  webhook_secret: string;
  event_filter: Array<'deployment.live' | 'deployment.failed' | 'rollout.completed' | 'rollout.aborted'>;
  retry_policy?: 'default' | 'aggressive' | 'none';
  delivery_format?: 'json' | 'cloudevents';
  enabled?: boolean;
};

