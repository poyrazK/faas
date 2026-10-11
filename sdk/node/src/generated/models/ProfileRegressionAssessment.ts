/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileAttributionComparison } from './ProfileAttributionComparison.js';
import type { ProfileCoverage } from './ProfileCoverage.js';
import type { ProfileQuery } from './ProfileQuery.js';
import type { ProfileRegressionEvidence } from './ProfileRegressionEvidence.js';
import type { ProfileRegressionMetric } from './ProfileRegressionMetric.js';
import type { ProfileRegressionOptions } from './ProfileRegressionOptions.js';
import type { ProfileRequestMixSnapshot } from './ProfileRequestMixSnapshot.js';
import type { ProfileRouteRegression } from './ProfileRouteRegression.js';
/**
 * Latest bounded historical CPU assessment, at most 64 KiB JSON, including at most 32 KiB evidence. Original samples are not retained. A different current saved revision marks this assessment stale. Profile expiry leaves this historical summary readable, without extending backend retention.
 */
export type ProfileRegressionAssessment = {
  readonly attribution?: ProfileAttributionComparison;
  readonly route_checks?: Array<ProfileRouteRegression>;
  readonly request_mix?: ProfileRequestMixSnapshot;
  investigation_revision: number;
  checked_at: string;
  status: 'regressed' | 'no_regression_detected' | 'inconclusive';
  reason: string;
  options: ProfileRegressionOptions;
  baseline: ProfileQuery;
  candidate: ProfileQuery;
  /**
   * Weighted observed request telemetry count for the baseline window.
   */
  baseline_requests?: number;
  /**
   * Weighted observed request telemetry count for the candidate window.
   */
  candidate_requests?: number;
  baseline_coverage?: ProfileCoverage;
  candidate_coverage?: ProfileCoverage;
  total?: ProfileRegressionMetric;
  evidence: Array<ProfileRegressionEvidence>;
  uncomparable_entries: number;
};

