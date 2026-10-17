/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetSpec } from './FinancialBudgetSpec.js';
/**
 * Saved policy intent. Active policies only notify (ADR-1500); no status stops or protects workloads.
 */
export type FinancialBudgetResponse = {
  id: string;
  account_id: string;
  revision: number;
  spec: FinancialBudgetSpec;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
  /**
   * active is an enabled notify budget; unavailable is enabled intent whose response is not integrated.
   */
  status: 'draft' | 'active' | 'unavailable' | 'deleted';
  /**
   * True when the selected response is integrated (monitored notify budgets); false for stopping responses until their runtime integrations and acceptance are complete.
   */
  enforcement_ready: boolean;
  reasons: Array<string>;
};

