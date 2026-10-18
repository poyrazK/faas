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
  event_filter?: Array<'operation.effect' | 'app.parked' | 'app.woken' | 'deployment.live' | 'deployment.failed' | 'rollout.completed' | 'rollout.aborted' | 'job.finished' | 'operation.finished' | 'usage_statement.finalized' | 'consumer.usage_threshold' | 'outbound.ai_budget.threshold' | 'issue.created' | 'issue.assigned' | 'issue.resolved' | 'issue.reopened' | 'issue.ignored' | 'issue.regressed' | 'issue.impact_threshold_reached' | 'issue.frequency_threshold_reached' | 'issue.tracker_sync.attention' | 'issue.tracker_sync.recovered' | 'debug.regression.detected' | 'debug.regression.resolved' | 'routes.requirements.violated' | 'routes.requirements.recovered' | 'routes.requirements.changed' | 'routes.health.blocked' | 'routes.health.resumed' | 'routes.health.aborted' | 'routes.monitor.violated' | 'routes.monitor.escalated' | 'routes.monitor.recovered' | 'workflow.finished' | 'automation.paused' | 'realtime.inbox.acknowledged' | 'realtime.inbox.gap' | 'realtime.inbox.fallback_required' | 'realtime.message.read' | 'realtime.notification.sent' | 'realtime.notification.failed' | 'realtime.notification.expired' | 'realtime.notification.cancelled' | 'realtime.notification.superseded' | 'realtime.schedule.published' | 'realtime.schedule.failed' | 'realtime.schedule.skipped' | 'app.health.changed' | 'event_recovery.completed' | 'event_recovery.cancelled' | 'event_recovery.expired' | 'profile.route_regressed' | 'profile.route_recovered' | 'profile.capture_ready' | 'profile.capture_oom_linked' | 'event_recovery.execution_finished' | 'realtime.channel.published' | 'realtime.channel.acknowledged' | 'realtime.consumer_group.dead_lettered' | 'realtime.consumer_group.dead_letter_expiring' | 'realtime.consumer_group.delivery_expired' | 'realtime.consumer_group.worker_lost' | 'realtime.consumer_group.worker_recovered' | 'realtime.consumer_group.circuit_opened' | 'realtime.consumer_group.circuit_half_open' | 'realtime.consumer_group.circuit_closed' | 'realtime.replay_job.completed' | 'realtime.replay_job.cancelled' | 'realtime.replay_job.processing_failed' | 'realtime.consumer_group.lag_exceeded' | 'realtime.consumer_group.lag_recovered' | 'event_recovery.execution_attention' | 'automation.recovery_review_ready' | 'automation.recovery_review_finished' | 'automation.recovery_policy_paused' | 'automation.recovery_queue_blocked' | 'automation.recovery_health_alert' | 'automation.recovery_health_recovered' | 'automation.recovery_polling_status'>;
  retry_policy?: 'default' | 'aggressive' | 'none';
  /**
   * Wire envelope for future deliveries; existing delivery rows are unchanged. Switching to datadog requires a new webhook_secret holding the Datadog API key.
   */
  delivery_format?: 'json' | 'cloudevents' | 'datadog';
  enabled?: boolean;
};

