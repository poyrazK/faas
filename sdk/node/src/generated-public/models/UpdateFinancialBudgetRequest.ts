/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetSpec } from './FinancialBudgetSpec.js';
/**
 * Complete replacement of budget intent guarded by its current revision.
 */
export type UpdateFinancialBudgetRequest = {
  expected_revision: number;
  spec: FinancialBudgetSpec;
};

