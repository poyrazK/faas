/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Data delivered with outbound.ai_budget.threshold when an AI budget reaches 80% or 100% in its period. alert_id is stable across retries; integration budgets notify every bound app, app budgets only that app, and an end user's or a tag's budget only the app whose call crossed it.
 */
export type OutboundAIBudgetThresholdWebhookPayload = {
  alert_id: string;
  integration_id: string;
  integration_name: string;
  budget: 'daily_tokens' | 'app_daily_tokens' | 'daily_cost' | 'monthly_cost' | 'user_daily_tokens' | 'user_daily_cost' | 'tag_daily_cost';
  app_id: string | null;
  /**
   * The X-Gregale-AI-User value whose daily budget crossed the threshold.
   */
  end_user: string | null;
  period_start: string;
  threshold_percent: 80 | 100;
  limit: number;
  used: number;
  crossed_at: string;
  /**
   * The tag whose daily cost limit crossed the threshold.
   */
  tag: string | null;
};

