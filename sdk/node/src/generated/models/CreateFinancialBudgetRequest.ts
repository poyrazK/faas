/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetSpec } from './FinancialBudgetSpec.js';
/**
 * New budget intent; activation supports monitored notify, monitored compute-only stop_previews, and app-scoped strict gross compute suspend_workloads with zero drain and no alert thresholds. Strict activation requires a quiet app, complete accounting, and native compute owners.
 */
export type CreateFinancialBudgetRequest = {
  spec: FinancialBudgetSpec;
};

