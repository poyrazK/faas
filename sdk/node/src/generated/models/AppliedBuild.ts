/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-workload build result from the apply-time build-enqueue loop.
 * On success: slug + app_id + deployment_id + build_id. On failure:
 * slug + app_id + error, no IDs. Wait-aware project deploys also
 * include the observed deployment and build lifecycle statuses.
 *
 */
export type AppliedBuild = {
  slug: string;
  app_id: string;
  deployment_id?: string;
  build_id?: string;
  /**
   * Observed deployment lifecycle status (for example live, failed, superseded, or cancelled) when the CLI waits.
   */
  deployment_status?: string;
  /**
   * Observed build lifecycle status (queued, running, succeeded, or failed) when the CLI waits.
   */
  build_status?: string;
  /**
   * Build failure class when the observed build failed.
   */
  failure_class?: string;
  /**
   * Staging or enqueue error message; partial-failure rows carry this in lieu of IDs.
   */
  error?: string;
};

