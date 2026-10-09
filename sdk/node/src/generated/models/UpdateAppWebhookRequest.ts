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
  /**
   * Replacement event selection; an empty array restores standard platform events. Include app.health.changed to opt into health notifications.
   */
  event_filter?: Array<'operation.effect' | 'app.parked' | 'app.woken' | 'deployment.live' | 'deployment.failed' | 'rollout.completed' | 'rollout.aborted' | 'job.finished' | 'operation.finished' | 'usage_statement.finalized' | 'issue.created' | 'issue.assigned' | 'issue.resolved' | 'issue.reopened' | 'issue.ignored' | 'issue.regressed' | 'issue.impact_threshold_reached' | 'debug.regression.detected' | 'debug.regression.resolved' | 'routes.requirements.violated' | 'routes.requirements.recovered' | 'routes.requirements.changed' | 'routes.health.blocked' | 'routes.health.resumed' | 'routes.health.aborted' | 'routes.monitor.violated' | 'routes.monitor.escalated' | 'routes.monitor.recovered' | 'workflow.finished' | 'automation.paused' | 'app.health.changed' | 'event_recovery.completed' | 'event_recovery.cancelled' | 'event_recovery.expired' | 'profile.route_regressed' | 'profile.route_recovered'>;
  retry_policy?: 'default' | 'aggressive' | 'none';
  /**
   * Wire envelope for future deliveries; existing delivery rows are unchanged.
   */
  delivery_format?: 'json' | 'cloudevents';
  enabled?: boolean;
};

