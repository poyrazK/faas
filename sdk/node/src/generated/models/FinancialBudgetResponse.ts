/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetSpec } from './FinancialBudgetSpec.js';
/**
 * Saved policy intent; draft and unavailable policies do not protect workloads.
 */
export type FinancialBudgetResponse = {
  id: string;
  account_id: string;
  revision: number;
  spec: FinancialBudgetSpec;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
  status: 'draft' | 'unavailable' | 'deleted';
  /**
   * False until runtime integrations and acceptance are complete.
   */
  enforcement_ready: boolean;
  reasons: Array<string>;
};

