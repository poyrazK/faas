/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryNotificationRetryResult } from './EventRecoveryNotificationRetryResult.js';
/**
 * Durable metadata-only receiver decisions, sorted by delivery ID and retained with the owning recovery job. Retrying identical intent returns this original response, including skipped decisions and decision time.
 */
export type EventRecoveryNotificationRetryResponse = {
  job_id: string;
  app_id: string;
  request_id: string;
  decided_at: string;
  results: Array<EventRecoveryNotificationRetryResult>;
};

