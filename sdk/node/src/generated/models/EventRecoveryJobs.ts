/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryJob } from './EventRecoveryJob.js';
export type EventRecoveryJobs = {
  jobs: Array<EventRecoveryJob>;
  /**
   * Omitted on the final page. Preserve the same app and filters; page size may change.
   */
  next_cursor?: string;
};

