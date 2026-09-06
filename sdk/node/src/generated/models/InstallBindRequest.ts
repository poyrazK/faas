/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for both `POST /v1/install/repos/list` and
 * `POST /v1/apps/{slug}/install/bind`. Carries the
 * (installation_id, repo_full_name, production_branch) tuple
 * the dashboard's bind picker commits. `production_branch` is
 * optional — when omitted, githubd uses the install's
 * `default_branch` from `/installations/{id}`.
 *
 */
export type InstallBindRequest = {
  installation_id: number;
  repo_full_name: string;
  production_branch?: string;
  /**
   * GitHub branch names mapped to deployment environment scopes. An empty object clears existing rules.
   */
  deploy_branches?: Record<string, string>;
};

