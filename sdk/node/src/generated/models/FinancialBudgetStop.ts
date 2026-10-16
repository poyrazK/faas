/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetEvaluation } from './FinancialBudgetEvaluation.js';
import type { FinancialBudgetStopTarget } from './FinancialBudgetStopTarget.js';
/**
 * Latched monitored compute threshold decision. Corrections cannot resume work. Policy revisions release only their own hold; in-flight teardown may finish. New matching previews are held too. Metering, scheduler and teardown delay can exceed the limit.
 */
export type FinancialBudgetStop = {
  id: string;
  policy_id: string;
  revision: number;
  period_start: string;
  period_end: string;
  requested_at: string;
  drain_deadline: string;
  resume_rule: 'manual' | 'next_period';
  evaluation: FinancialBudgetEvaluation;
  targets: Array<FinancialBudgetStopTarget>;
};

