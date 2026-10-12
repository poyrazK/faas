/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Data delivered with outbound.ai_budget.threshold when an AI budget reaches 80% or 100% in its period (ADR-991). alert_id is stable across retries; integration budgets notify every bound app, app budgets only that app.
 */
export type OutboundAIBudgetThresholdWebhookPayload = {
  alert_id: string;
  integration_id: string;
  integration_name: string;
  budget: 'daily_tokens' | 'app_daily_tokens' | 'daily_cost' | 'monthly_cost';
  app_id: string | null;
  period_start: string;
  threshold_percent: 80 | 100;
  limit: number;
  used: number;
  crossed_at: string;
};

