/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Live app attached to a durable project and its latest release state.
 */
export type ProjectWorkloadResponse = {
  /**
   * Immutable compute region of the workload app. Environment clones preserve this placement.
   */
  compute_region?: string;
  slug: string;
  workload_name: string;
  status: string;
  deployment_status?: string;
  rollout_state?: string;
  build_status?: string;
};

