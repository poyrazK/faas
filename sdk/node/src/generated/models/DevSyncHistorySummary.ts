/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Aggregate trend and regression guidance for recent developer syncs.
 */
export type DevSyncHistorySummary = {
  count: number;
  within_slo_count: number;
  p50_edit_to_live_ms: number;
  p95_edit_to_live_ms: number;
  slo_target_ms: number;
  slowest_phase?: string;
  slowest_phase_ms?: number;
  guidance?: string;
};

