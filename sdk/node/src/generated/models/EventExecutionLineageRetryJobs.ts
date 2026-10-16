/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventExecutionLineageRetryJobAttentionPage } from './EventExecutionLineageRetryJobAttentionPage.js';
import type { EventExecutionLineageRetryJobSummary } from './EventExecutionLineageRetryJobSummary.js';
/**
 * Scoped live page of retained bulk lineage retry job summaries.
 */
export type EventExecutionLineageRetryJobs = {
  attention?: EventExecutionLineageRetryJobAttentionPage;
  jobs: Array<EventExecutionLineageRetryJobSummary>;
  /**
   * Continuation cursor; omitted when no further page was observed.
   */
  next_after?: string;
};

