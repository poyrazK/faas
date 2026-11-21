/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagExperimentArm } from './FlagExperimentArm.js';
import type { FlagExperimentComparison } from './FlagExperimentComparison.js';
/**
 * One variant compared with control.
 */
export type FlagExperimentVariantResult = (FlagExperimentArm & {
  /**
   * Absolute difference in conversion rate against control.
   */
  lift?: number;
  relative_lift?: number;
  /**
   * Lower bound of the 95% confidence sequence on lift.
   */
  low?: number;
  /**
   * Upper bound of the 95% confidence sequence on lift.
   */
  high?: number;
  /**
   * Always-valid p-value (running minimum).
   */
  p_value?: number;
  status: 'significant' | 'not_significant' | 'insufficient_data' | 'sample_ratio_mismatch';
  unadjusted?: FlagExperimentComparison;
  /**
   * A bandit arm's posterior probability of being best; bandits report no p-values.
   */
  best_probability?: number;
  /**
   * Conversion rate a bandit gives up by choosing this arm.
   */
  expected_loss?: number;
});

