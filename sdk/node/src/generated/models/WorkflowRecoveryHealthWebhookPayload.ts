/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Historical automation.recovery_health_alert or automation.recovery_health_recovered transition with stable event and incident identities. Recovery targets only original alert receivers still enabled and subscribed to recovery at capture. Delivery can occur after the observed condition changes and does not authorize retries or release slots.
 */
export type WorkflowRecoveryHealthWebhookPayload = {
  event_id: string;
  episode_id: string;
  app_id: string;
  app: string;
  policy_version: number;
  status: 'healthy' | 'inactive' | 'paused' | 'blocked' | 'stalled' | 'unknown';
  reason: string;
  opened_at: string;
  observed_at: string;
  pending: number;
  outstanding: number;
  queue_truncated: boolean;
  health_url: string;
};

