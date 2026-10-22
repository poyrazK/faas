/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One deployment currently serving traffic.
 */
export type DebugStatusDeployment = {
  id: string;
  traffic_percent: number;
  rollout_state?: string;
  commit_sha?: string;
  created_at: string;
};

