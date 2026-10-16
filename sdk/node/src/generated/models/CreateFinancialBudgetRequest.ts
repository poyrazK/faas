/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetSpec } from './FinancialBudgetSpec.js';
/**
 * New budget intent; enabled policies must use monitored notify. Stopping and strict policies remain disabled drafts.
 */
export type CreateFinancialBudgetRequest = {
  spec: FinancialBudgetSpec;
};

