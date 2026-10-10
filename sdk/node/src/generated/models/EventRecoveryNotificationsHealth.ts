/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryNotificationHealthCounts } from './EventRecoveryNotificationHealthCounts.js';
import type { EventRecoveryNotificationJobHealth } from './EventRecoveryNotificationJobHealth.js';
/**
 * Bounded retained notification candidates, oldest admission completion first. Candidate-set completeness is separate from phase evidence completeness. Counts describe jobs and can overlap; reads do not capture or retry notifications.
 */
export type EventRecoveryNotificationsHealth = {
  coverage: 'bounded_retained_notification_jobs';
  observed_jobs: number;
  counts_complete: boolean;
  job_limit: 50;
  overdue_grace_seconds: number;
  admission: EventRecoveryNotificationHealthCounts;
  execution: EventRecoveryNotificationHealthCounts;
  jobs: Array<EventRecoveryNotificationJobHealth>;
};

