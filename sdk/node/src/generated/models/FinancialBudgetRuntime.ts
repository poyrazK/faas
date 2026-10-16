/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetAlert } from './FinancialBudgetAlert.js';
import type { FinancialBudgetEvaluation } from './FinancialBudgetEvaluation.js';
/**
 * Current policy revision and UTC month only; a missing evaluation means monitoring has not evaluated that revision in this period.
 */
export type FinancialBudgetRuntime = {
  evaluation?: FinancialBudgetEvaluation;
  alerts: Array<FinancialBudgetAlert>;
};

