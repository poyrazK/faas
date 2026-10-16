/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable named-environment purge target and stable retry identity; path must be explicit even for whole-environment purging.
 */
export type CreateEnvironmentCachePurgeRequest = {
  app_id: string;
  project_id: string;
  environment_id: string;
  /**
   * Keep this purge operation UUID and body on retries.
   */
  request_id: string;
  /**
   * Exact decoded URL path; empty string selects the whole immutable environment host. Globs, query strings, controls, escapes and dot segments are invalid.
   */
  path: string;
};

