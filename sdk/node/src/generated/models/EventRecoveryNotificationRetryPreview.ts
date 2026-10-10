/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryNotificationRetryCandidate } from './EventRecoveryNotificationRetryCandidate.js';
/**
 * Advisory current receiver eligibility for a retained recovery job. Admission and execution entries remain separate. Missing evidence cannot create a receiver or authorize replay.
 */
export type EventRecoveryNotificationRetryPreview = {
  job_id: string;
  app_id: string;
  observed_at: string;
  counts_complete: boolean;
  receivers: Array<EventRecoveryNotificationRetryCandidate>;
};

