/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryNotificationRetryDecisionSummary } from './EventRecoveryNotificationRetryDecisionSummary.js';
/**
 * One saved retry request found in an owned retained app recovery job, with original-generation outcome summary and metadata-only inspection paths.
 */
export type EventRecoveryNotificationRetryBacklogRequest = {
  job_id: string;
  job_created_at: string;
  summary: EventRecoveryNotificationRetryDecisionSummary;
  detail_path: string;
  retry_preview_path: string;
};

