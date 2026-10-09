/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileAttributionComparison } from './ProfileAttributionComparison.js';
import type { ProfileCanaryGateState } from './ProfileCanaryGateState.js';
import type { ProfileCoverage } from './ProfileCoverage.js';
import type { ProfileQuery } from './ProfileQuery.js';
import type { ProfileRegressionEvidence } from './ProfileRegressionEvidence.js';
import type { ProfileRegressionMetric } from './ProfileRegressionMetric.js';
import type { ProfileRegressionOptions } from './ProfileRegressionOptions.js';
import type { ProfileRequestMixSnapshot } from './ProfileRequestMixSnapshot.js';
import type { ProfileRouteRegression } from './ProfileRouteRegression.js';
import type { ProfileSource } from './ProfileSource.js';
/**
 * Latest retained sampled CPU comparison for a deployment's canary stages, produced by a background worker using the app's enabled automatic profile policy and equal fixed windows. The stage and policy revision identify the assessment. Route-health reads return the saved result and never query profile storage. Checks are advisory unless canary_gate is explicitly configured. Gate evidence requires consecutive distinct qualified route windows; timeout behavior is configured explicitly. Completed assessments are retained for 30 days.
 */
export type CanaryProfileSignal = {
  gate?: ProfileCanaryGateState;
  readonly attribution?: ProfileAttributionComparison;
  readonly route_checks?: Array<ProfileRouteRegression>;
  readonly request_mix?: ProfileRequestMixSnapshot;
  mode: 'report_only' | 'gate';
  status: 'queued' | 'running' | 'regressed' | 'no_regression_detected' | 'inconclusive' | 'cancelled';
  reason: string;
  canary_step: number;
  canary_step_started_at: string;
  created_at: string;
  /**
   * Last completed assessment attempt, absent until the worker has queried both profile windows.
   */
  checked_at?: string;
  attempts: number;
  /**
   * Scheduled first check or retry time while the result is pending.
   */
  next_attempt_at?: string;
  completed_at?: string;
  policy_revision: number;
  metric: 'cpu_per_second' | 'cpu_per_request';
  window_seconds: number;
  options: ProfileRegressionOptions;
  baseline?: ProfileQuery;
  candidate?: ProfileQuery;
  baseline_requests?: number;
  candidate_requests?: number;
  baseline_coverage?: ProfileCoverage;
  candidate_coverage?: ProfileCoverage;
  /**
   * Request-time source revision for the stable deployment. Commit URLs are derived from recorded deployment provenance and are not retained with the assessment.
   */
  readonly baseline_source?: ProfileSource;
  /**
   * Request-time source revision for the canary deployment. Commit URLs are derived from recorded deployment provenance and are not retained with the assessment.
   */
  readonly candidate_source?: ProfileSource;
  /**
   * Dashboard link to load the exact stable and canary profile windows for a completed assessment.
   */
  readonly comparison_url?: string;
  total?: ProfileRegressionMetric;
  evidence: Array<ProfileRegressionEvidence>;
  uncomparable_entries: number;
};

