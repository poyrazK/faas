/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Counts of conditional preparation decisions and observed application pressure for the sampled candidates.
 */
export type WorkflowRecoveryPolicyEvaluationTotals = {
  candidates: number;
  would_prepare: number;
  excluded: number;
  unknown: number;
  cooldown: number;
  preparation_budget: number;
  deduplicated: number;
  execution_budget_pressure: number;
  execution_cooldown_pressure: number;
};

