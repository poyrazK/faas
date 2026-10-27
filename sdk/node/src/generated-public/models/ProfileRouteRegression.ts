/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileRegressionCPUPerRequestMetric } from './ProfileRegressionCPUPerRequestMetric.js';
import type { ProfileRegressionEvidence } from './ProfileRegressionEvidence.js';
import type { ProfileRouteLabelComparison } from './ProfileRouteLabelComparison.js';
/**
 * Retained advisory route-associated sampled CPU/request observation. Missing attribution, sparse requests or inadequate capture coverage produces insufficient_data. No rollout decision is changed.
 */
export type ProfileRouteRegression = {
  /**
   * Availability and limitations of route-filtered code attribution.
   */
  code_reason?: string;
  /**
   * Bounded route-filtered function self CPU and inclusive call-path increases ranked by CPU/request delta. Source URLs are derived at read time.
   */
  code_evidence?: Array<ProfileRegressionEvidence>;
  readonly label_coverage?: ProfileRouteLabelComparison;
  route: string;
  status: 'regressed' | 'no_regression_detected' | 'insufficient_data';
  reason: string;
  baseline_requests?: number;
  candidate_requests?: number;
  metric?: ProfileRegressionCPUPerRequestMetric;
  /**
   * Request-time dashboard link to the route differential flamegraph for the frozen comparison windows.
   */
  readonly comparison_url?: string;
};

