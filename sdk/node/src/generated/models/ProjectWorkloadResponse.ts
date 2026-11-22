/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Live app attached to a durable project and its latest release state.
 */
export type ProjectWorkloadResponse = {
  slug: string;
  workload_name: string;
  status: string;
  deployment_status?: string;
  rollout_state?: string;
  /**
   * The latest deployment's share of the service's traffic while it is live. Omitted when it serves none.
   */
  traffic_percent?: number;
  build_status?: string;
  /**
   * The project services this one declares it calls (Compose depends_on), sorted. Omitted when none.
   */
  depends_on?: Array<string>;
};

