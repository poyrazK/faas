/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One recovery issue with evidence and the next available operator action.
 */
export type WorkflowRecoveryOverviewIssue = {
  scope: 'automatic_policy' | 'automatic_queue' | 'manual_initial' | 'manual_retry';
  reason: string;
  policy_version: number;
  review_id?: string;
  job_id?: string;
  retry_at?: string;
  inspection_url: string;
};

