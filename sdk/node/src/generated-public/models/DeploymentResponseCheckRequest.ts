/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit identity and one GET path; named scopes require project_id and environment_id.
 */
export type DeploymentResponseCheckRequest = {
  app_id: string;
  scope: string;
  project_id?: string;
  environment_id?: string;
  /**
   * App-relative GET path with optional query; no fragment or controls.
   */
  path: string;
};

