/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRecordingCoverageItem } from './ObsRecordingCoverageItem.js';
import type { ObsRecordingDependencyMapping } from './ObsRecordingDependencyMapping.js';
import type { ObsRuleCoverageSet } from './ObsRuleCoverageSet.js';
import type { ObsRuleCoverageTotals } from './ObsRuleCoverageTotals.js';
/**
 * Recording definition coverage with separately scoped expected syntactic alert dependencies.
 */
export type ObsRecordingCoverage = {
  generated_at: string;
  scope: string;
  fingerprint_algorithm: string;
  selection_source: 'none' | 'explicit_request';
  state: 'covered' | 'degraded' | 'partial' | 'unknown';
  reason?: 'expected_set_not_selected' | 'expected_catalog_unavailable' | 'expected_set_unavailable' | 'prometheus_not_configured' | 'source_unavailable' | 'deadline_exceeded' | 'expected_rules_differ' | 'comparison_incomplete';
  /**
   * Recording expectation profile metadata; null before selection or when its catalog cannot be loaded.
   */
  expected_set: (ObsRuleCoverageSet | null);
  /**
   * Recording profile choices embedded in this binary; choices do not establish installed inventory.
   */
  available_sets: Array<ObsRuleCoverageSet>;
  /**
   * Unknown without a comparable selected recording source; a complete source with zero rows has known missing rules.
   */
  totals: (ObsRuleCoverageTotals | null);
  /**
   * Recording comparisons sorted by ambiguity then change then absence then unknown then match and finally identity hash.
   */
  items: Array<ObsRecordingCoverageItem>;
  total_items: number;
  limit: number;
  truncated: boolean;
  /**
   * Independent expected syntax mapping available once a valid catalog set is selected; null without a usable selection.
   */
  dependencies: (ObsRecordingDependencyMapping | null);
  limitations: Array<'expectation_selected_per_request_not_deployment_evidence' | 'selected_set_is_a_minimum_not_an_exclusive_inventory' | 'recording_definitions_not_evaluation_or_metric_health' | 'dependencies_are_expected_catalog_syntax_not_proven_impact' | 'nonexact_selectors_duplicate_names_and_cycles_leave_uncertainty' | 'missing_recording_labels_are_empty_per_prometheus_wire_contract' | 'group_intervals_offsets_annotations_and_source_paths_not_compared' | 'raw_queries_labels_and_errors_excluded' | 'previews_bounded_totals_cover_complete_inputs'>;
};

