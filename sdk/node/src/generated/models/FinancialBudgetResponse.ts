/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetRuntime } from './FinancialBudgetRuntime.js';
import type { FinancialBudgetSpec } from './FinancialBudgetSpec.js';
/**
 * Saved policy intent with current-revision monitoring, alert delivery and preview stop acknowledgements. Notifications alone never establish workload stopping.
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
   * True when the selected stopping integration is available; actual per-target teardown is reported in runtime.stops. Strict compute balances are reported in runtime.compute.
   */
  enforcement_ready: boolean;
  /**
   * True for an enabled supported monitored policy; inspect runtime for evaluation freshness and delivery status.
   */
  notification_ready?: boolean;
  runtime?: FinancialBudgetRuntime;
  reasons: Array<string>;
};

