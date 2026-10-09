/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryNotificationRetryDecision } from './EventRecoveryNotificationRetryDecision.js';
/**
 * Saved per receiver decision for one retry request, with a separate timestamp for the read of current retained delivery state.
 */
export type EventRecoveryNotificationRetryDecisionDetail = {
  job_id: string;
  app_id: string;
  request_id: string;
  decided_at: string;
  current_status_observed_at: string;
  decisions: Array<EventRecoveryNotificationRetryDecision>;
};

