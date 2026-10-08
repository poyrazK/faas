/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryItem } from './EventRecoveryItem.js';
export type EventRecoveryPreview = {
  observed_at: string;
  coverage: 'captured_application_recipients' | 'retained_application_executions';
  /**
   * Exact up to the job limit; a lower bound when exceeds_job_limit is true.
   */
  matched_count: number;
  exceeds_job_limit: boolean;
  sample: Array<EventRecoveryItem>;
};

