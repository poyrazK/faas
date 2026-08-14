/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * POST /v1/runs/{run_id}/tasks/{idx}/retry body. By default
 * the per-task attempt counter is reset to 0; pass
 * `reset_attempt: false` to increment the existing counter
 * instead.
 *
 */
export type RetryTaskRequest = {
  reset_attempt?: boolean | null;
};

