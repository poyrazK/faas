/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryPolicyDecision } from './WorkflowRecoveryPolicyDecision.js';
/**
 * Payload of automation.recovery_review_ready. Carries frozen prepared targets and review commands; approval and application remain explicit. Retries reuse the event identifier and delivery ledger.
 */
export type WorkflowRecoveryReviewReadyWebhookPayload = {
  /**
   * Captured initial failure for parentless reviews. Parent-job reviews carry job_id instead.
   */
  initial_failure_id?: string;
  event_id: string;
  review_id: string;
  app_id: string;
  app: string;
  job_id?: string;
  policy_version: number;
  prepared_at: string;
  observed_at: string;
  targets: Array<WorkflowRecoveryPolicyDecision>;
  review_url: string;
  status_url: string;
  review_command: string;
  export_command: string;
};

