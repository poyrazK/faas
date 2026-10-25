/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Double-submit CSRF envelope for customer-facing GitHub connection
 * mutations. The token is returned by GET /v1/apps/{slug}/install or
 * GET /v1/apps/{slug}/install/bind and must match the named
 * `faas_csrf_github_install` cookie.
 *
 */
export type GitHubInstallMutationRequest = {
  csrf_token: string;
};

