/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The first settlement that reached a budget threshold in its period (ADR-991). limit and used are tokens for token budgets and millicents for cost budgets.
 */
export type OutboundAIBudgetAlert = {
  id: string;
  integration_id: string;
  budget: 'daily_tokens' | 'app_daily_tokens' | 'daily_cost' | 'monthly_cost' | 'user_daily_tokens' | 'user_daily_cost' | 'tag_daily_cost';
  app_id: string | null;
  /**
   * The end user of a user_daily_tokens or user_daily_cost alert (ADR-995).
   */
  end_user: string | null;
  period_start: string;
  threshold_percent: 80 | 100;
  limit: number;
  used: number;
  crossed_at: string;
  /**
   * The tag of a tag_daily_cost alert (ADR-1438).
   */
  tag: string | null;
};

