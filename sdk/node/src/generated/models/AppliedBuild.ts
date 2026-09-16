/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-workload build result from the apply-time build-enqueue loop.
 * On success: slug + app_id + deployment_id + build_id. On failure:
 * slug + app_id + error, no IDs.
 *
 */
export type AppliedBuild = {
  slug: string;
  app_id: string;
  deployment_id?: string;
  build_id?: string;
  /**
   * Terminal deployment status added by wait-aware CLI responses.
   */
  deployment_status?: string;
  /**
   * Terminal build status added by wait-aware CLI responses.
   */
  build_status?: string;
  /**
   * Staging or enqueue error message; partial-failure rows carry this in lieu of IDs.
   */
  error?: string;
};

