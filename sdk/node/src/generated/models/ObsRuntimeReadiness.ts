/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeReadinessCounts } from './ObsRuntimeReadinessCounts.js';
import type { ObsRuntimeReadinessReasonCount } from './ObsRuntimeReadinessReasonCount.js';
/**
 * Advisory readiness for the same inspected targets; independent of revision matching and qualification. Omitted by older servers.
 */
export type ObsRuntimeReadiness = {
  state: 'ready' | 'not_ready' | 'incomplete' | 'unknown';
  reason?: 'fresh_not_ready' | 'incomplete_readiness_evidence' | 'readiness_unavailable';
  counts: ObsRuntimeReadinessCounts;
  reasons: Array<ObsRuntimeReadinessReasonCount>;
};

