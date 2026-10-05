/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetSpec } from './FinancialBudgetSpec.js';
/**
 * Immutable intent audit written in the same transaction as the policy.
 */
export type FinancialBudgetRevisionResponse = {
  policy_id: string;
  revision: number;
  /**
   * Authenticated account or API key identity; never a supplied actor value.
   */
  actor: string;
  mutation: 'created' | 'updated' | 'deleted';
  spec: FinancialBudgetSpec;
  recorded_at: string;
};

