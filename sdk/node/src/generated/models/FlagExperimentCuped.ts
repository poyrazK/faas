/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * CUPED variance reduction on the results; reason says why it was not applied.
 */
export type FlagExperimentCuped = {
  applied: boolean;
  /**
   * Days before the start that make each customer's covariate.
   */
  pre_days: number;
  theta?: number;
  /**
   * Share of the metric's variance removed, 0 to 1.
   */
  variance_reduction?: number;
  reason?: 'off' | 'pre_period_pending' | 'no_pre_period_variance' | 'no_pre_period_data' | 'not_supported';
};

