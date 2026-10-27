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
  /**
   * Events to subscribe to; omit or leave empty for standard platform events. Select app.health.changed explicitly to enable health notifications.
   */
  event_filter?: Array<'operation.effect' | 'app.parked' | 'app.woken' | 'deployment.live' | 'deployment.failed' | 'rollout.completed' | 'rollout.aborted' | 'job.finished' | 'operation.finished' | 'outbound.ai_budget.threshold' | 'outbound.ai_quality.drift' | 'issue.created' | 'issue.assigned' | 'issue.resolved' | 'issue.reopened' | 'issue.ignored' | 'issue.regressed' | 'issue.impact_threshold_reached' | 'issue.frequency_threshold_reached' | 'issue.compound_threshold_reached' | 'issue.tracker_sync.attention' | 'issue.tracker_sync.recovered' | 'debug.regression.detected' | 'debug.regression.resolved' | 'routes.requirements.violated' | 'routes.requirements.recovered' | 'routes.requirements.changed' | 'routes.health.blocked' | 'routes.health.resumed' | 'routes.health.aborted' | 'routes.monitor.violated' | 'routes.monitor.escalated' | 'routes.monitor.recovered' | 'routes.insight.detected' | 'routes.insight.resolved' | 'workflow.finished' | 'automation.paused' | 'app.health.changed' | 'event_recovery.completed' | 'event_recovery.cancelled' | 'event_recovery.expired' | 'profile.route_regressed' | 'profile.route_recovered' | 'profile.capture_ready' | 'profile.capture_oom_linked' | 'event_recovery.execution_finished' | 'realtime.channel.published' | 'event_recovery.execution_attention' | 'automation.recovery_review_ready' | 'automation.recovery_review_finished' | 'automation.recovery_policy_paused' | 'automation.recovery_queue_blocked' | 'automation.recovery_health_alert' | 'automation.recovery_health_recovered' | 'automation.recovery_polling_status'>;
  retry_policy?: 'default' | 'aggressive' | 'none';
  /**
   * Wire envelope. json preserves the legacy Gregale body; cloudevents opts into CloudEvents 1.0 structured mode; datadog posts Datadog Events API bodies to a Datadog Events endpoint and sends webhook_secret as DD-API-KEY, for deployment and rollout events only.
   */
  delivery_format?: 'json' | 'cloudevents' | 'datadog';
  enabled?: boolean;
};

