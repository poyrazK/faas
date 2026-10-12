/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One latest application-reported outcome among eligible workflows associated with this rule version. Up to 16 outcomes are returned in code order. Basis points are rounded down, so displayed proportions may sum below 10000 even without truncation.
 */
export type OperationWorkflowRuleVersionOutcome = {
  outcome_code: string;
  workflow_count: number;
  proportion_basis_points: number;
};

