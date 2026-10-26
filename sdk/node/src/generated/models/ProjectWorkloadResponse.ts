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
  build_status?: string;
  /**
   * The project services this one declares it calls (Compose depends_on), sorted. Omitted when none (ADR-20261026193018).
   */
  depends_on?: Array<string>;
};

