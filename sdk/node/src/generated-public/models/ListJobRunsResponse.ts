/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { JobRunResponse } from './JobRunResponse.js';
/**
 * Page of runs for a job.
 */
export type ListJobRunsResponse = {
  runs: Array<JobRunResponse>;
  limit: number;
  offset: number;
  next_offset: number;
  total: number;
};

