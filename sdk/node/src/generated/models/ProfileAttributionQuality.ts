/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileAttributionReason } from './ProfileAttributionReason.js';
/**
 * Labeled and unattributed shares of all sampled CPU in the deployment window, even when a route filter is selected. Not VM CPU or request instrumentation completeness. Percentages are absent when no CPU was sampled.
 */
export type ProfileAttributionQuality = {
  available: boolean;
  total_cpu_seconds: number;
  attributed_cpu_seconds: number;
  unattributed_cpu_seconds: number;
  attributed_percent?: number;
  unattributed_percent?: number;
  /**
   * Host discard counters reconciled against the whole merged CPU capture. False for missing or inconsistent counters.
   */
  diagnostics_complete: boolean;
  reasons: Array<ProfileAttributionReason>;
};

