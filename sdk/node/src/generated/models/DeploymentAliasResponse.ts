/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A customer's named pointer to one per-app deployment revision.
 */
export type DeploymentAliasResponse = {
  name: string;
  deployment_id: string;
  /**
   * Per-app revision number; rendered in CLI output as vN.
   */
  revision: number;
  created_at: string;
  updated_at: string;
};

