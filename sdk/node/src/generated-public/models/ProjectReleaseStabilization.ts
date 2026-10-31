/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in bounded health monitoring after progressive rollout success.
 */
export type ProjectReleaseStabilization = {
  enabled: boolean;
  status: 'waiting' | 'monitoring' | 'completed' | 'rolled_back' | 'needs_operator' | 'conflicted';
  health?: 'healthy' | 'regressed' | 'unknown';
  started_at?: string;
  deadline?: string;
};

