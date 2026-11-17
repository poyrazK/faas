/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Counts only fresh unambiguous ready or not-ready evidence; no raw readiness text.
 */
export type ObsRuntimeReadinessReasonCount = {
  reason: 'ready' | 'draining' | 'database' | 'vmmd' | 'grpc' | 'storage' | 'credentials' | 'stale' | 'process' | 'other';
  count: number;
};

