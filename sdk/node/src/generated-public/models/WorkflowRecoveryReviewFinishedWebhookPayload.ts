/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryReviewStatus } from './WorkflowRecoveryReviewStatus.js';
/**
 * Payload of automation.recovery_review_finished. All actions have proven final outcomes for their frozen generations. This historical result remains valid after review decisions or later retries; receivers deduplicate by event_id.
 */
export type WorkflowRecoveryReviewFinishedWebhookPayload = {
  event_id: string;
  review_id: string;
  app_id: string;
  app: string;
  job_id: string;
  policy_version: number;
  captured_at: string;
  result: WorkflowRecoveryReviewStatus;
  review_url: string;
  status_url: string;
  status_command: string;
};

