/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for POST /v1/apps/{slug}/delayed-tasks. scheduled_at must be in the future (UTC).
 */
export type DelayedTaskRequest = {
  payload?: Record<string, any>;
  scheduled_at: string;
};

