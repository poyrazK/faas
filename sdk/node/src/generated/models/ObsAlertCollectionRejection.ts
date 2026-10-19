/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuleHealthItem } from './ObsRuleHealthItem.js';
import type { ObsRuleHealthTotals } from './ObsRuleHealthTotals.js';
/**
 * Sanitized observations from the exact rejected source; complete counts and an unhealthy-first preview, bounded to 16 KiB of compact JSON.
 */
export type ObsAlertCollectionRejection = {
  scope: 'rejected_snapshot';
  /**
   * Distinguishes observed rule issues, a known empty loaded inventory, and rejection without a matching rule-level diagnostic.
   */
  reason: 'rule_diagnostics_present' | 'no_alert_rules_observed' | 'collector_validation_failed';
  totals: ObsRuleHealthTotals;
  /**
   * Unhealthy rules and invalid empty groups, ordered by issue priority then hashed group and item identity; ages refer to attempted_at.
   */
  items: Array<ObsRuleHealthItem>;
  /**
   * Complete count of unhealthy diagnostic items before the persisted preview is bounded.
   */
  total_items: number;
  limit: number;
  /**
   * Whether the item or byte bound omitted diagnostic items included in complete totals.
   */
  truncated: boolean;
};

