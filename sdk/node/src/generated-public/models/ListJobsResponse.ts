/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { JobResponse } from './JobResponse.js';
/**
 * Page of jobs on the account (issue
 */
export type ListJobsResponse = {
  jobs: Array<JobResponse>;
  limit: number;
  offset: number;
  /**
   * -1 = last page
   */
  next_offset: number;
  total: number;
};

