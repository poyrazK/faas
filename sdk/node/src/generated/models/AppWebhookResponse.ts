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
  /**
   * Subscribed event names. Empty means summary events; issue.handoff requires explicit opt-in to detailed evidence. Its payload follows IssueHandoff.
   */
  event_filter: Array<'app.parked' | 'app.woken' | 'deployment.live' | 'deployment.failed' | 'rollout.completed' | 'rollout.aborted' | 'job.finished' | 'usage_statement.finalized' | 'issue.created' | 'issue.assigned' | 'issue.resolved' | 'issue.reopened' | 'issue.ignored' | 'issue.regressed' | 'issue.impact_threshold_reached' | 'issue.handoff' | 'debug.regression.detected' | 'debug.regression.resolved' | 'routes.requirements.violated' | 'routes.requirements.recovered' | 'routes.requirements.changed' | 'routes.health.blocked' | 'routes.health.resumed' | 'routes.health.aborted'>;
  retry_policy: 'default' | 'aggressive' | 'none';
  delivery_format: 'json' | 'cloudevents';
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

