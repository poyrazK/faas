/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryNotificationRetryDecisionSummary } from './EventRecoveryNotificationRetryDecisionSummary.js';
import type { EventRecoveryNotificationRetryHistoryTotals } from './EventRecoveryNotificationRetryHistoryTotals.js';
/**
 * Retained explicit retry decisions for one recovery job, optionally filtered by status and newest first. The job stores at most 100 decisions and pruning removes this history.
 */
export type EventRecoveryNotificationRetryHistory = {
  job_id: string;
  app_id: string;
  observed_at: string;
  /**
   * Number of returned request summaries matching the optional status filter.
   */
  matched_count: number;
  totals: EventRecoveryNotificationRetryHistoryTotals;
  decisions: Array<EventRecoveryNotificationRetryDecisionSummary>;
};

