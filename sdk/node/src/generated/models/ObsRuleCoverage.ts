/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuleCoverageItem } from './ObsRuleCoverageItem.js';
import type { ObsRuleCoverageSet } from './ObsRuleCoverageSet.js';
import type { ObsRuleCoverageTotals } from './ObsRuleCoverageTotals.js';
/**
 * Selected expected alert definitions compared with a complete current source; covered does not imply healthy evaluations or inputs.
 */
export type ObsRuleCoverage = {
  generated_at: string;
  scope: string;
  fingerprint_algorithm: string;
  selection_source: 'none' | 'explicit_request';
  state: 'covered' | 'degraded' | 'partial' | 'unknown';
  reason?: 'expected_set_not_selected' | 'expected_catalog_unavailable' | 'expected_set_unavailable' | 'prometheus_not_configured' | 'source_unavailable' | 'deadline_exceeded' | 'expected_rules_differ' | 'comparison_incomplete';
  /**
   * Selected versioned set metadata; null when no selection or its catalog is unavailable.
   */
  expected_set: (ObsRuleCoverageSet | null);
  /**
   * Built-in choices from this binary; their presence is not deployment intent.
   */
  available_sets: Array<ObsRuleCoverageSet>;
  /**
   * Null for unselected or unreadable evidence; a complete empty source instead has known missing definitions.
   */
  totals: (ObsRuleCoverageTotals | null);
  /**
   * Expected definitions ordered by ambiguity then change then absence then unknown then match; identity hashes break ties.
   */
  items: Array<ObsRuleCoverageItem>;
  total_items: number;
  limit: number;
  truncated: boolean;
  limitations: Array<'expectation_selected_per_request_not_deployment_evidence' | 'selected_set_is_a_minimum_not_an_exclusive_inventory' | 'alert_rules_only_recording_rules_not_checked' | 'coverage_not_evaluation_metric_or_notification_health' | 'canonical_syntax_not_semantic_equivalence' | 'group_intervals_offsets_annotations_and_source_paths_not_compared' | 'raw_queries_labels_and_errors_excluded' | 'preview_bounded_totals_cover_complete_source'>;
};

