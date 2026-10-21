/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetAlert } from './FinancialBudgetAlert.js';
import type { FinancialBudgetEvaluation } from './FinancialBudgetEvaluation.js';
import type { FinancialBudgetStop } from './FinancialBudgetStop.js';
import type { FinancialComputeBalance } from './FinancialComputeBalance.js';
/**
 * Evaluation and alerts cover the current revision and UTC month. Active manual stop decisions can carry over from earlier months until a policy mutation releases that revision.
 */
export type FinancialBudgetRuntime = {
  compute?: FinancialComputeBalance;
  evaluation?: FinancialBudgetEvaluation;
  alerts: Array<FinancialBudgetAlert>;
  projection?: FinancialBudgetAlert;
  stops?: Array<FinancialBudgetStop>;
};

