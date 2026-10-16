/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Pinned original execution timestamp, immutable target and exact new schedule identity/time; action is taken from the original record.
 */
export type RescheduleEnvironmentCacheRequest = {
  replacement_id: string;
  app_id: string;
  project_id: string;
  environment_id: string;
  expected_execute_at: string;
  execute_at: string;
};

