/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplyResponse } from './ApplyResponse.js';
/**
 * Durable, account-scoped recovery for one project apply attempt.
 */
export type ProjectSubmissionReceipt = {
  /**
   * Whether the selected workload set was durably recorded before enqueue.
   */
  selection_complete: boolean;
  attempt_id: string;
  project_slug: string;
  environment?: string;
  status: 'processing' | 'complete' | 'rejected' | 'interrupted';
  apply: ApplyResponse;
};

