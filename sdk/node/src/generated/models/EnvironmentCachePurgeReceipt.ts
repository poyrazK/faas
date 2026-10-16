/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stable durable logical cache-purge intent for one immutable environment host.
 */
export type EnvironmentCachePurgeReceipt = {
  app_id: string;
  project_id: string;
  environment_id: string;
  scope: string;
  request_id: string;
  path: string;
  generation: number;
  requested_at: string;
};

