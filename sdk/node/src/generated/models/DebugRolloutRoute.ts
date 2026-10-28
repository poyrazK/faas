/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One route's candidate-vs-stable comparison over the rollout window; counts are represented requests.
 */
export type DebugRolloutRoute = {
  route: string;
  candidate_requests: number;
  candidate_server_errors: number;
  candidate_p95_ms: number;
  stable_requests: number;
  stable_server_errors: number;
  stable_p95_ms: number;
};

