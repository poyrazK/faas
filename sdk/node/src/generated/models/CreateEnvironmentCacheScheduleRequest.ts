/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateEnvironmentCachePurgeRequest } from './CreateEnvironmentCachePurgeRequest.js';
import type { SetEnvironmentCachePolicyRequest } from './SetEnvironmentCachePolicyRequest.js';
/**
 * One durable environment cache action; exactly one policy or purge is required, with request_id equal to schedule_id.
 */
export type CreateEnvironmentCacheScheduleRequest = {
  schedule_id: string;
  app_id: string;
  project_id: string;
  environment_id: string;
  execute_at: string;
  policy?: SetEnvironmentCachePolicyRequest;
  purge?: CreateEnvironmentCachePurgeRequest;
};

