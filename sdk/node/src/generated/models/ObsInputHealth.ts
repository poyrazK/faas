/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsInputJob } from './ObsInputJob.js';
/**
 * Critical telemetry sample evidence for a selected minimum deployment role expectation.
 */
export type ObsInputHealth = {
  generated_at: string;
  scope: string;
  expected_role: '' | 'single-box-v1' | 'control-plane-v1';
  available_roles: Array<'single-box-v1' | 'control-plane-v1'>;
  state: 'healthy' | 'degraded' | 'partial' | 'unknown';
  reason?: 'expected_role_not_selected' | 'expected_role_unavailable';
  max_age_seconds: number;
  lookback_seconds: number;
  /**
   * Fixed selected job inventory; empty before role selection, with full aggregate counts and no target preview.
   */
  jobs: Array<ObsInputJob>;
  limitations: Array<'role_selected_per_request_not_deployment_evidence' | 'static_expectations_are_minimum_one_not_full_inventory' | 'discovery_counts_not_target_identity_membership' | 'default_scrape_job_collision_uses_exported_job' | 'readiness_sample_presence_not_readiness_value' | 'metric_checks_cover_fresh_up_targets_only' | 'one_vetted_idle_safe_gauge_per_selected_job_not_all_rule_inputs' | 'historical_samples_bounded_by_lookback' | 'raw_labels_queries_urls_and_errors_excluded'>;
};

