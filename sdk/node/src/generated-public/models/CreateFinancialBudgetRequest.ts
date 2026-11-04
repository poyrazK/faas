/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetSpec } from './FinancialBudgetSpec.js';
/**
 * New budget intent; activation supports monitored notify, monitored compute-only stop_previews, and strict suspend_workloads (app/job gross compute or account net compute optionally combined with egress) with zero drain and no alert thresholds. Strict activation requires quiet covered workloads, complete accounting, and native owners. Account strict activation also requires recorded allowances and native owners capable of funded jobs. Combining compute and egress additionally requires complete egress accounting and byte-capable native owners.
 */
export type CreateFinancialBudgetRequest = {
  spec: FinancialBudgetSpec;
};

