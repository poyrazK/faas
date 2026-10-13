/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventExecutionLineageRetryJobSummary } from './EventExecutionLineageRetryJobSummary.js';
export type EventExecutionLineageRetryJobs = {
  jobs: Array<EventExecutionLineageRetryJobSummary>;
  /**
   * Continuation cursor; omitted when no further page was observed.
   */
  next_after?: string;
};

