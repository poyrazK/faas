/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for both `POST /v1/install/repos/list` and
 * `POST /v1/apps/{slug}/install/bind`. Carries the
 * (installation_id, repo_full_name, production_branch) tuple
 * the dashboard's bind picker commits. `production_branch` is
 * optional — when omitted, the GitHub integration uses the install's
 * `default_branch` from `/installations/{id}`.
 * Binding requires a `csrf_token` issued for `bind_app_to_repo`
 * by `GET /v1/auth/csrf` and its matching HttpOnly cookie.
 *
 */
export type InstallBindRequest = {
  installation_id: number;
  repo_full_name: string;
  /**
   * Action-bound CSRF token required by the browser bind mutation; not required for repository listing.
   */
  csrf_token?: string;
  production_branch?: string;
  /**
   * GitHub branch names mapped to deployment environment scopes. An empty object clears existing rules.
   */
  deploy_branches?: Record<string, string>;
};

