/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Successful bind. `binding_id` is the deterministic `bind-<appID>-<repo>` form used in audit log entries.
 */
export type InstallBindResponse = {
  binding_id: string;
  repo_full_name: string;
  production_branch: string;
  deploy_branches?: Record<string, string>;
};

