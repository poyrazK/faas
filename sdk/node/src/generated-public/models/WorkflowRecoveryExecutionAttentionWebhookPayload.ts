/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryExecutionAttention } from './WorkflowRecoveryExecutionAttention.js';
/**
 * First execution attention signal captured for a native recovery job with frozen notification receivers.
 */
export type WorkflowRecoveryExecutionAttentionWebhookPayload = {
  event_id: string;
  job_id: string;
  app_id: string;
  observed_at: string;
  attention: WorkflowRecoveryExecutionAttention;
  items_url: string;
};

