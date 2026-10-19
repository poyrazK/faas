/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsInputTargetCounts } from './ObsInputTargetCounts.js';
/**
 * Configured scrape coverage with known failures retained independently of unknown expectations.
 */
export type ObsInputTargets = {
  state: 'healthy' | 'degraded' | 'partial' | 'unknown';
  reason?: 'source_unavailable' | 'invalid_evidence' | 'scrape_coverage_gap' | 'inventory_count_mismatch' | 'expectation_unavailable';
  /**
   * Complete strict scrape vector counts; null when source evidence is unavailable or invalid.
   */
  counts: (ObsInputTargetCounts | null);
};

