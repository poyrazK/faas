/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetRuntime } from './FinancialBudgetRuntime.js';
import type { FinancialBudgetSpec } from './FinancialBudgetSpec.js';
/**
 * Saved policy intent with current-revision UTC-month monitoring and alert delivery state. Active notification policies do not stop workloads.
 */
export type FinancialBudgetResponse = {
  id: string;
  account_id: string;
  revision: number;
  spec: FinancialBudgetSpec;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
  status: 'draft' | 'unavailable' | 'active' | 'deleted';
  /**
   * False until runtime integrations and acceptance are complete.
   */
  enforcement_ready: boolean;
  /**
   * True for an enabled monitored notification policy; inspect runtime for evaluation freshness and delivery status.
   */
  notification_ready?: boolean;
  runtime?: FinancialBudgetRuntime;
  reasons: Array<string>;
};

