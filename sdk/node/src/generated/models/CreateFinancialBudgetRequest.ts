/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetSpec } from './FinancialBudgetSpec.js';
/**
 * New budget intent; activation supports monitored notify, monitored compute-only stop_previews, and strict compute suspend_workloads (app/job gross_usage or account net_usage) with zero drain and no alert thresholds. Strict activation requires quiet covered workloads, complete accounting, and native owners. Account strict activation also requires recorded allowance and native owners capable of funded jobs.
 */
export type CreateFinancialBudgetRequest = {
  spec: FinancialBudgetSpec;
};

