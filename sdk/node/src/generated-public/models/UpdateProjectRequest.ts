/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Patch for the customer-managed repository binding and production branch.
 */
export type UpdateProjectRequest = {
  /**
   * GitHub owner/name. An empty string removes the repository binding.
   */
  repo_full_name?: string;
  production_branch?: string;
};

