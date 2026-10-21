/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuleHealthItem } from './ObsRuleHealthItem.js';
import type { ObsRuleHealthTotals } from './ObsRuleHealthTotals.js';
/**
 * Point-in-time loaded-rule diagnostics with complete source totals and a bounded unhealthy-first preview.
 */
export type ObsRuleHealthResponse = {
  generated_at: string;
  scope: string;
  state: 'healthy' | 'degraded' | 'disabled' | 'unavailable';
  reason?: string;
  max_age_seconds: number;
  clock_skew_tolerance_seconds: number;
  /**
   * Null for unconfigured or failed source reads; a complete empty source has known zero rules and unavailable state.
   */
  totals: (ObsRuleHealthTotals | null);
  /**
   * Invalid items precede invalid clocks then failures then stale then unavailable then healthy; group and rule hashes break ties.
   */
  items: Array<ObsRuleHealthItem>;
  total_items: number;
  limit: number;
  truncated: boolean;
  limitations: Array<string>;
};

