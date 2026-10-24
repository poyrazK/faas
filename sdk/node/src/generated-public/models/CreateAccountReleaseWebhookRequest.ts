/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Subscribe to account release outcomes and environment GitOps status transitions.
 */
export type CreateAccountReleaseWebhookRequest = {
  target_url: string;
  webhook_secret: string;
  event_filter: Array<'deployment.live' | 'deployment.failed' | 'rollout.completed' | 'rollout.aborted' | 'environment.gitops.changed'>;
  retry_policy?: 'default' | 'aggressive' | 'none';
  delivery_format?: 'json' | 'cloudevents';
  enabled?: boolean;
};

