/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryNotificationRetryDecisionSummary } from './EventRecoveryNotificationRetryDecisionSummary.js';
/**
 * All retained explicit retry decisions for one recovery job, newest first. The job stores at most 100 decisions and pruning removes this history.
 */
export type EventRecoveryNotificationRetryHistory = {
  job_id: string;
  app_id: string;
  observed_at: string;
  decisions: Array<EventRecoveryNotificationRetryDecisionSummary>;
};

