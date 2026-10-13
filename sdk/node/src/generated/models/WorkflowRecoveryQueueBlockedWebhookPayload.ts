/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Historical automation.recovery_queue_blocked event captured once per automatic retry action when fresh inspection cannot resolve pending or applied evidence. Capturing this event does not finalize a child, release its execution slot, or authorize another retry. Delivery may occur after the block resolves; stable identity and frozen recipients support receiver deduplication.
 */
export type WorkflowRecoveryQueueBlockedWebhookPayload = {
  event_id: string;
  app_id: string;
  app: string;
  review_id: string;
  action_id: string;
  policy_version: number;
  position: number;
  child_job_id?: string;
  status: 'pending' | 'applied';
  reason: 'evidence_unknown';
  observed_at: string;
  queue_url: string;
  review_status_url: string;
};

