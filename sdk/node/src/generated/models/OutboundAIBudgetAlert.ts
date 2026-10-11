/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The first settlement that reached a budget threshold in its period (ADR-984). limit and used are tokens for token budgets and millicents for cost budgets.
 */
export type OutboundAIBudgetAlert = {
  id: string;
  integration_id: string;
  budget: 'daily_tokens' | 'app_daily_tokens' | 'daily_cost' | 'monthly_cost';
  app_id: string | null;
  period_start: string;
  threshold_percent: 80 | 100;
  limit: number;
  used: number;
  crossed_at: string;
};

