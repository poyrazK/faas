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
  event_filter: Array<'operation.effect' | 'app.parked' | 'app.woken' | 'deployment.live' | 'deployment.failed' | 'rollout.completed' | 'rollout.aborted' | 'job.finished' | 'operation.finished' | 'usage_statement.finalized' | 'issue.created' | 'issue.assigned' | 'issue.resolved' | 'issue.reopened' | 'issue.ignored' | 'issue.regressed' | 'issue.impact_threshold_reached' | 'debug.regression.detected' | 'debug.regression.resolved' | 'routes.requirements.violated' | 'routes.requirements.recovered' | 'routes.requirements.changed' | 'routes.health.blocked' | 'routes.health.resumed' | 'routes.health.aborted' | 'routes.monitor.violated' | 'routes.monitor.escalated' | 'routes.monitor.recovered' | 'workflow.finished' | 'realtime.inbox.acknowledged' | 'realtime.inbox.gap' | 'realtime.inbox.fallback_required' | 'realtime.message.read' | 'realtime.notification.sent' | 'realtime.notification.failed' | 'realtime.notification.expired' | 'realtime.notification.cancelled' | 'realtime.notification.superseded' | 'realtime.schedule.published' | 'realtime.schedule.failed' | 'realtime.schedule.skipped'>;
  retry_policy: 'default' | 'aggressive' | 'none';
  delivery_format: 'json' | 'cloudevents';
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

