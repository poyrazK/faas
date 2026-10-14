/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current matching-page observations; no total count or frozen membership is promised.
 */
export type EventExecutionLineageRetryJobAttentionPage = {
  coverage: 'live_matching_retry_jobs_page';
  filter: 'any' | 'stalled' | 'retention_risk';
  observed_at: string;
  /**
   * Another matching row was observed after this page; consistent with next_after.
   */
  has_more: boolean;
  inactivity_warning_seconds: number;
  retention_warning_seconds: number;
};

