/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryJob } from './EventRecoveryJob.js';
/**
 * Paginated retained recovery jobs with admission progress and account-bound discovery cursor.
 */
export type EventRecoveryJobs = {
  jobs: Array<EventRecoveryJob>;
  /**
   * Omitted on the final page. Preserve the same app and filters; page size may change.
   */
  next_cursor?: string;
};

