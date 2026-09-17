/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { JobRunResponse } from './JobRunResponse.js';
import type { JobTaskResponse } from './JobTaskResponse.js';
/**
 * POST .../tasks/{idx}/retry response. The task is re-queued with capped backoff.
 */
export type JobTaskRetryResponse = {
  task: JobTaskResponse;
  run: JobRunResponse;
  retried_at: string;
  next_attempt_at: string;
};

