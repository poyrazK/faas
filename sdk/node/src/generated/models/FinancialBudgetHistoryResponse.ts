/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetRevisionResponse } from './FinancialBudgetRevisionResponse.js';
/**
 * Immutable policy revision page and an optional exclusive continuation cursor.
 */
export type FinancialBudgetHistoryResponse = {
  revisions: Array<FinancialBudgetRevisionResponse>;
  next_revision?: number;
};

