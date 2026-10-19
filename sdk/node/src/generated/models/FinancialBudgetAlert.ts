/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetEvaluation } from './FinancialBudgetEvaluation.js';
/**
 * One threshold event per policy revision and UTC month. Edits re-evaluate monthly spending. Delivery is at least once with a stable transport idempotency key; delivered means accepted by the configured transport, not confirmed inbox receipt.
 */
export type FinancialBudgetAlert = {
  id: string;
  policy_revision: number;
  threshold_millicents: number;
  evaluation: FinancialBudgetEvaluation;
  state: 'pending' | 'sending' | 'retrying' | 'delivered';
  attempts: number;
  next_attempt_at: string;
  delivered_at?: string;
  last_error?: 'mail_delivery_failed';
};

