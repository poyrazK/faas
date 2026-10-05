/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetSpec } from './FinancialBudgetSpec.js';
import type { FinancialBudgetTarget } from './FinancialBudgetTarget.js';
/**
 * Known spending, evidence gaps, workload consequences and explicit readiness.
 */
export type FinancialBudgetPreviewResponse = {
  spec: FinancialBudgetSpec;
  period_start: string;
  period_end: string;
  as_of: string;
  known_millicents: number;
  /**
   * Known subtotal is at or above the limit; inspect coverage before inferring complete spending.
   */
  known_limit_reached: boolean;
  coverage_complete: boolean;
  fresh: boolean;
  reasons: Array<string>;
  /**
   * False while durable decisions and owner integrations lack acceptance; preview never activates a policy.
   */
  enforcement_ready: boolean;
  guarantee: string;
  targets: Array<FinancialBudgetTarget>;
  continuing_targets: Array<FinancialBudgetTarget>;
};

