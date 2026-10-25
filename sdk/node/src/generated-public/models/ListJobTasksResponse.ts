/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { JobTaskResponse } from './JobTaskResponse.js';
/**
 * Page of tasks for a job-run.
 */
export type ListJobTasksResponse = {
  tasks: Array<JobTaskResponse>;
  limit: number;
  offset: number;
  next_offset: number;
  total: number;
};

