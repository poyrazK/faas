/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { JobRunResponse } from './JobRunResponse.js';
/**
 * POST .../runs/{id}/cancel body. Returns the post-cancel run aggregate + cancelled_at timestamp.
 */
export type JobRunCancelledResponse = {
  run: JobRunResponse;
  cancelled_at: string;
};

