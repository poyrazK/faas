/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryNotificationRetryBacklogRequest } from './EventRecoveryNotificationRetryBacklogRequest.js';
import type { EventRecoveryNotificationRetryBacklogTotals } from './EventRecoveryNotificationRetryBacklogTotals.js';
/**
 * App-wide notification retry evidence from a bounded job page. Counts cover scanned jobs before filtering and never imply whole-app totals across unseen pages.
 */
export type EventRecoveryNotificationRetryBacklog = {
  app_id: string;
  observed_at: string;
  jobs_scanned: number;
  counts_scope: 'job_page';
  totals: EventRecoveryNotificationRetryBacklogTotals;
  matched_count: number;
  next_cursor?: string;
  requests: Array<EventRecoveryNotificationRetryBacklogRequest>;
};

