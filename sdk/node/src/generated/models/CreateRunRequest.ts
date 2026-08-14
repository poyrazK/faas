/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * POST /v1/jobs/{id}/runs body. `tasks` is required (1
 * ≤ tasks ≤ JobMaxTasksPerRun for the plan); `parallelism`
 * and `env_overrides` default to the job's configured
 * values.
 *
 */
export type CreateRunRequest = {
  tasks: number;
  parallelism?: number | null;
  env_overrides?: Record<string, string>;
};

